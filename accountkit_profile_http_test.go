package accountkit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const profileHTTPSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"bio":{"type":"string"},"score":{"type":"integer"},"read_note":{"type":"string","readOnly":true},"write_note":{"type":"string","writeOnly":true},"preferences":{"type":"object","properties":{"locale":{"type":"string"},"theme":{"type":"string"}},"additionalProperties":false},"tags":{"type":"array","items":{"type":"string"}},"payload":{"type":"string"}},"additionalProperties":false}`

func profileHTTPFixture(t *testing.T) (*accountkit.Auth, *pgxpool.Pool, *captureSender, *audit.Memory) {
	t.Helper()
	cfg := minimal()
	cfg.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(profileHTTPSchema)}
	rdb := testRedis(t)
	base, pool, sent := integrationInstance(t, cfg, rdb)
	if err := base.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	memory := &audit.Memory{}
	a, err := accountkit.New(base.Config(), accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: memory, RequestID: func(r *http.Request) string { return r.Header.Get("X-Request-Id") }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, pool, sent, memory
}

func profileHTTPRequest(ctx context.Context, h http.Handler, method, path, body, bearer, requestID string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-Id", requestID)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertProfileHTTP(t *testing.T, w *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("profile status=%d want=%d cache=%q body=%s", w.Code, status, w.Header().Get("Cache-Control"), w.Body.String())
	}
	if reason != "" && !strings.Contains(w.Body.String(), `"reason":"`+reason+`"`) {
		t.Fatalf("missing reason %s: %s", reason, w.Body.String())
	}
}

func assertProfileResource(t *testing.T, w *httptest.ResponseRecorder, id, want string) {
	t.Helper()
	assertProfileHTTP(t, w, 200, "")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("profile response is not JSON")
	}
	var resource struct {
		Name       string          `json:"name"`
		Attributes json.RawMessage `json:"profile_attributes"`
	}
	decoder := json.NewDecoder(w.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resource); err != nil {
		t.Fatal(err)
	}
	if resource.Name != "users/"+id+"/profile" {
		t.Fatalf("unexpected resource name %q", resource.Name)
	}
	// UseNumber catches float64 precision loss at the HTTP boundary.
	decode := func(raw []byte) any {
		var result any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		if err := d.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !reflect.DeepEqual(decode(resource.Attributes), decode([]byte(want))) {
		t.Fatalf("profile=%s want=%s", resource.Attributes, want)
	}
}

func profileHTTPUpdateEvents(memory *audit.Memory) []audit.Event {
	var result []audit.Event
	for _, event := range memory.Events() {
		if event.Action == "USER_PROFILE_ATTRIBUTES_UPDATED" {
			result = append(result, event)
		}
	}
	return result
}

func profileHTTPSnapshot(t *testing.T, pool *pgxpool.Pool, id string) (string, time.Time) {
	t.Helper()
	var data string
	var updated time.Time
	if err := pool.QueryRow(t.Context(), `SELECT profile_attributes::text, update_time FROM user_account WHERE id=$1`, id).Scan(&data, &updated); err != nil {
		t.Fatal(err)
	}
	return data, updated
}

func TestProfileAttributesHTTPIntegration(t *testing.T) {
	a, pool, sent, memory := profileHTTPFixture(t)
	first := policyEmailSignIn(t, a, sent, "profile-http-one@example.test", "profile-http-one").Tokens
	second := policyEmailSignIn(t, a, sent, "profile-http-two@example.test", "profile-http-two").Tokens
	h := a.EndUserHandler()
	call := func(method, path, body, token, requestID string) *httptest.ResponseRecorder {
		return profileHTTPRequest(t.Context(), h, method, path, body, token, requestID)
	}
	const path = "/users/me/profile"
	assertProfileResource(t, call("GET", path, "", first.AccessToken, "initial"), first.UserID, `{}`)
	const initial = `{"bio":"synthetic-private-bio","score":9007199254740993,"read_note":"synthetic-read-only","write_note":"synthetic-write-only","preferences":{"locale":"en","theme":"dark"},"tags":["one","two"]}`
	assertProfileResource(t, call("PATCH", path, `{"profile_attributes":`+initial+`}`, first.AccessToken, "profile-initial"), first.UserID, initial)
	assertProfileResource(t, call("GET", path, "", first.AccessToken, "read"), first.UserID, initial)
	const merged = `{"score":9007199254740993,"read_note":"synthetic-read-only","write_note":"synthetic-write-only","preferences":{"locale":"zh-CN","theme":"dark"},"tags":["replacement"]}`
	assertProfileResource(t, call("PATCH", path, `{"profile_attributes":{"bio":null,"preferences":{"locale":"zh-CN"},"tags":["replacement"]}}`, first.AccessToken, "profile-merged"), first.UserID, merged)
	before, updated := profileHTTPSnapshot(t, pool, first.UserID)
	assertProfileResource(t, call("PATCH", path, `{"profile_attributes":{}}`, first.AccessToken, "profile-noop"), first.UserID, merged)
	assertProfileHTTP(t, call("PATCH", path, `{"profile_attributes":{"score":9007199254740993e0}}`, first.AccessToken, "profile-number-noop"), 200, "")
	for _, tc := range []struct{ name, body, reason string }{
		{"unknown deletion", `{"profile_attributes":{"synthetic-unknown-secret":null}}`, "INVALID_PROFILE_ATTRIBUTES"},
		{"inner duplicate", `{"profile_attributes":{"bio":"one","bio":"two"}}`, "INVALID_PROFILE_ATTRIBUTES"},
		{"schema", `{"profile_attributes":{"score":"synthetic-private-value"}}`, "INVALID_PROFILE_ATTRIBUTES"},
		{"raw budget", `{"profile_attributes":{` + strings.Repeat(" ", 16384) + `}}`, "INVALID_PROFILE_ATTRIBUTES"},
		{"oversized payload", `{"profile_attributes":{"payload":"` + strings.Repeat("x", 16384) + `"}}`, "INVALID_PROFILE_ATTRIBUTES"},
		{"missing", `{}`, "MALFORMED_BODY"},
		{"null", `{"profile_attributes":null}`, "INVALID_PROFILE_ATTRIBUTES"},
		{"other user", `{"profile_attributes":{},"user_id":"` + second.UserID + `"}`, "MALFORMED_BODY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := call("PATCH", path, tc.body, first.AccessToken, "profile-rejected")
			assertProfileHTTP(t, w, 400, tc.reason)
			for _, secret := range []string{"synthetic-unknown-secret", "synthetic-private-value"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("error leaked private input")
				}
			}
		})
	}
	after, afterTime := profileHTTPSnapshot(t, pool, first.UserID)
	if before != after || !updated.Equal(afterTime) {
		t.Fatal("read, no-op or rejected PATCH changed stored data/time")
	}
	assertProfileResource(t, call("GET", path+"?user_id="+first.UserID, "", second.AccessToken, "second-read"), second.UserID, `{}`)
	assertProfileResource(t, call("PATCH", path+"?user_id="+first.UserID, `{"profile_attributes":{"bio":"second-user"}}`, second.AccessToken, "profile-second"), second.UserID, `{"bio":"second-user"}`)
	assertProfileResource(t, call("GET", path, "", first.AccessToken, "first-again"), first.UserID, merged)
	if w := call("GET", "/users/"+first.UserID+"/profile", "", second.AccessToken, "other-path"); w.Code != 404 {
		t.Fatalf("other-user route returned %d", w.Code)
	}
	// Change only this disposable fixture's state; keep real authentication active
	// so the HTTP request reaches the domain's lifecycle checks.
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted} {
		if _, err := pool.Exec(t.Context(), `UPDATE user_account SET state=$2 WHERE id=$1`, first.UserID, state); err != nil {
			t.Fatal(err)
		}
		read := call("GET", path, "", first.AccessToken, "state-read")
		if state == enum.UserFrozen {
			assertProfileResource(t, read, first.UserID, merged)
		} else {
			assertProfileHTTP(t, read, 400, "INVALID_ACCOUNT_STATE")
		}
		assertProfileHTTP(t, call("PATCH", path, `{"profile_attributes":{"bio":"state-rejected"}}`, first.AccessToken, "state-write"), 400, "INVALID_ACCOUNT_STATE")
	}
	after, afterTime = profileHTTPSnapshot(t, pool, first.UserID)
	if before != after || !updated.Equal(afterTime) {
		t.Fatal("state rejection changed stored profile/time")
	}
	events := profileHTTPUpdateEvents(memory)
	if len(events) != 3 {
		t.Fatalf("successful changed PATCH events=%d want=3", len(events))
	}
	for i, requestID := range []string{"profile-initial", "profile-merged", "profile-second"} {
		e := events[i]
		if e.Context.RequestID != requestID || e.Actor.Type != audit.ActorService || e.Actor.ID != "accountkit-host" {
			t.Fatalf("audit identity/context lost: %+v", e)
		}
		raw, _ := json.Marshal(e)
		for _, secret := range []string{"synthetic-private-bio", "synthetic-read-only", "synthetic-write-only", "9007199254740993", "second-user"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("audit leaked profile value")
			}
		}
	}
}

func TestProfileAttributesHTTPCapacity(t *testing.T) {
	a, pool, sent, memory := profileHTTPFixture(t)
	signed := policyEmailSignIn(t, a, sent, "profile-http-capacity@example.test", "profile-http-capacity").Tokens
	h := a.EndUserHandler()
	document := `{"payload":"` + strings.Repeat("x", 16384-len(`{"payload":""}`)) + `"}`
	call := func(patch string) *httptest.ResponseRecorder {
		return profileHTTPRequest(t.Context(), h, "PATCH", "/users/me/profile", `{"profile_attributes":`+patch+`}`, signed.AccessToken, "capacity")
	}
	assertProfileResource(t, call(document), signed.UserID, document)
	assertProfileResource(t, profileHTTPRequest(t.Context(), h, "GET", "/users/me/profile", "", signed.AccessToken, "capacity-read"), signed.UserID, document)
	before, updated := profileHTTPSnapshot(t, pool, signed.UserID)
	// Each raw patch fits, but merging another field exceeds the full-document budget.
	assertProfileHTTP(t, call(`{"bio":"a"}`), 400, "INVALID_PROFILE_ATTRIBUTES")
	// Raw bytes count before normalization, even when whitespace would disappear.
	assertProfileHTTP(t, call(document+" "), 400, "INVALID_PROFILE_ATTRIBUTES")
	after, afterTime := profileHTTPSnapshot(t, pool, signed.UserID)
	if before != after || !updated.Equal(afterTime) || len(profileHTTPUpdateEvents(memory)) != 1 {
		t.Fatal("HTTP capacity rejection changed profile/time or recorded an update")
	}
}

func startProfileHTTPPatch(ctx context.Context, h http.Handler, bearer, patch, requestID string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- profileHTTPRequest(ctx, h, "PATCH", "/users/me/profile", `{"profile_attributes":`+patch+`}`, bearer, requestID)
	}()
	return done
}

func waitProfileHTTPBlocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker uint32, want int, pending ...<-chan *httptest.ResponseRecorder) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		if err := pool.QueryRow(ctx, `WITH RECURSIVE blocked(pid) AS (
 SELECT pid FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))
 UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
) SELECT count(*) FROM blocked`, blocker).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= want {
			return
		}
		for _, done := range pending {
			select {
			case result := <-done:
				t.Fatalf("HTTP PATCH finished before observed account lock: %d %s", result.Code, result.Body.String())
			default:
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("HTTP account lock not observed:", ctx.Err())
		case <-ticker.C:
		}
	}
}

func finishProfileHTTPPatch(t *testing.T, ctx context.Context, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatal("HTTP PATCH did not finish:", ctx.Err())
		return nil
	}
}

func lockProfileHTTPAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err = tx.Exec(ctx, `SELECT id FROM user_account WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	return tx
}

func TestProfileAttributesHTTPConcurrency(t *testing.T) {
	a, pool, sent, memory := profileHTTPFixture(t)
	signed := policyEmailSignIn(t, a, sent, "profile-http-concurrent@example.test", "profile-http-concurrent").Tokens
	h := a.EndUserHandler()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lock := lockProfileHTTPAccount(t, ctx, pool, signed.UserID)
	first := startProfileHTTPPatch(ctx, h, signed.AccessToken, `{"bio":"concurrent"}`, "concurrent-bio")
	second := startProfileHTTPPatch(ctx, h, signed.AccessToken, `{"preferences":{"locale":"zh-CN"}}`, "concurrent-locale")
	waitProfileHTTPBlocked(t, ctx, pool, lock.Conn().PgConn().PID(), 2, first, second)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertProfileHTTP(t, finishProfileHTTPPatch(t, ctx, first), 200, "")
	assertProfileHTTP(t, finishProfileHTTPPatch(t, ctx, second), 200, "")
	assertProfileResource(t, profileHTTPRequest(ctx, h, "GET", "/users/me/profile", "", signed.AccessToken, "concurrent-read"), signed.UserID, `{"bio":"concurrent","preferences":{"locale":"zh-CN"}}`)
	if len(profileHTTPUpdateEvents(memory)) != 2 {
		t.Fatal("concurrent successful changes lost their audits")
	}
	// Commit a lifecycle transition ahead of the observed HTTP lock waiter.
	before, updated := profileHTTPSnapshot(t, pool, signed.UserID)
	lock = lockProfileHTTPAccount(t, ctx, pool, signed.UserID)
	rejected := startProfileHTTPPatch(ctx, h, signed.AccessToken, `{"bio":"must-not-commit"}`, "state-race")
	waitProfileHTTPBlocked(t, ctx, pool, lock.Conn().PgConn().PID(), 1, rejected)
	if _, err := lock.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, signed.UserID, enum.UserPendingDeletion); err != nil {
		t.Fatal(err)
	}
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertProfileHTTP(t, finishProfileHTTPPatch(t, ctx, rejected), 400, "INVALID_ACCOUNT_STATE")
	after, afterTime := profileHTTPSnapshot(t, pool, signed.UserID)
	if before != after || !updated.Equal(afterTime) || len(profileHTTPUpdateEvents(memory)) != 2 {
		t.Fatal("lock waiter used stale lifecycle state or audited a rejection")
	}
}
