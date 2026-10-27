package purgefeed_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/purgefeed"
	"github.com/bbxx111/accountkit/user"
)

func secret(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

// fakeFeed 模拟按 seq 游标排列的已清理记录：第 i 条的 seq 为 i+1。
type fakeFeed struct {
	records []user.PurgedUser
	err     error
	calls   int
	lastCtx context.Context
}

func (f *fakeFeed) list(ctx context.Context, after int64, limit int) ([]user.PurgedUser, int64, error) {
	f.calls++
	f.lastCtx = ctx
	if f.err != nil {
		return nil, after, f.err
	}
	if after < 0 || limit < 1 || limit > user.MaxPurgedUserPage {
		return nil, after, fmt.Errorf("%w: bad", user.ErrInvalidArgument)
	}
	if after > int64(len(f.records)) {
		return nil, after, user.ErrPurgedUserCursorAhead
	}
	var out []user.PurgedUser
	next := after
	for i := int(after); i < len(f.records) && len(out) < limit; i++ {
		out = append(out, f.records[i])
		next = int64(i + 1)
	}
	return out, next, nil
}

func records(n int) []user.PurgedUser {
	base := time.Date(2026, 10, 27, 8, 0, 0, 0, time.UTC)
	out := make([]user.PurgedUser, n)
	for i := range out {
		out[i] = user.PurgedUser{UserID: fmt.Sprintf("u_%013d", i+1), PurgeTime: base.Add(time.Duration(i) * time.Minute)}
	}
	return out
}

func get(h http.Handler, query, id, password string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v1/purgedUsers"+query, nil)
	if id != "" {
		r.SetBasicAuth(id, password)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type page struct {
	PurgedUsers []struct {
		UserID    string `json:"user_id"`
		PurgeTime string `json:"purge_time"`
	} `json:"purged_users"`
	NextPageToken string `json:"next_page_token"`
}

func decode(t *testing.T, w *httptest.ResponseRecorder) page {
	t.Helper()
	var p page
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	return p
}

func reason(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct{ Status, Reason string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	return b.Error.Status + "/" + b.Error.Reason
}

func TestPurgeFeedPaging(t *testing.T) {
	feed := &fakeFeed{records: records(5)}
	h, err := purgefeed.New(map[string][]string{"ai-food": {secret(1)}}, feed.list)
	if err != nil {
		t.Fatal(err)
	}
	w := get(h, "?page_size=2", "ai-food", secret(1))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("first page: %d %v", w.Code, w.Header())
	}
	p := decode(t, w)
	if len(p.PurgedUsers) != 2 || p.PurgedUsers[0].UserID != "u_0000000000001" || p.PurgedUsers[0].PurgeTime != "2026-10-27T08:00:00Z" || p.NextPageToken == "" {
		t.Fatalf("first page: %+v", p)
	}
	if _, deadline := feed.lastCtx.Deadline(); !deadline {
		t.Fatal("lookup must be bounded")
	}
	var all []string
	token := ""
	for range 10 {
		w = get(h, "?page_size=2&page_token="+token, "ai-food", secret(1))
		p = decode(t, w)
		if w.Code != 200 || p.NextPageToken == "" {
			t.Fatalf("page: %d %+v", w.Code, p)
		}
		if len(p.PurgedUsers) == 0 {
			if p.NextPageToken != token {
				t.Fatalf("caught-up token changed: %q -> %q", token, p.NextPageToken)
			}
			break
		}
		for _, u := range p.PurgedUsers {
			all = append(all, u.UserID)
		}
		token = p.NextPageToken
	}
	if len(all) != 5 {
		t.Fatalf("paged %v", all)
	}
	// 空列表以 [] 而非 null 返回；默认页大小 100。
	empty := &fakeFeed{}
	h2, _ := purgefeed.New(map[string][]string{"ai-food": {secret(1)}}, empty.list)
	if w := get(h2, "", "ai-food", secret(1)); w.Code != 200 || !strings.Contains(w.Body.String(), `"purged_users":[]`) {
		t.Fatalf("empty: %d %s", w.Code, w.Body)
	}
}

func TestPurgeFeedRejectsBadInput(t *testing.T) {
	feed := &fakeFeed{records: records(3)}
	h, _ := purgefeed.New(map[string][]string{"ai-food": {secret(1)}}, feed.list)
	for name, tc := range map[string]struct {
		query, want string
	}{
		"page size zero":      {"?page_size=0", "INVALID_ARGUMENT/INVALID_ARGUMENT"},
		"page size too large": {"?page_size=1001", "INVALID_ARGUMENT/INVALID_ARGUMENT"},
		"page size text":      {"?page_size=ten", "INVALID_ARGUMENT/INVALID_ARGUMENT"},
		"tampered token":      {"?page_token=not-a-token", "INVALID_ARGUMENT/INVALID_PAGE_TOKEN"},
		"negative token":      {"?page_token=" + base64.RawURLEncoding.EncodeToString([]byte("v1:-1")), "INVALID_ARGUMENT/INVALID_PAGE_TOKEN"},
		"non canonical token": {"?page_token=" + base64.RawURLEncoding.EncodeToString([]byte("v1:007")), "INVALID_ARGUMENT/INVALID_PAGE_TOKEN"},
		"wrong version token": {"?page_token=" + base64.RawURLEncoding.EncodeToString([]byte("v2:1")), "INVALID_ARGUMENT/INVALID_PAGE_TOKEN"},
		"duplicate parameter": {"?page_size=1&page_size=2", "INVALID_ARGUMENT/INVALID_ARGUMENT"},
	} {
		before := feed.calls
		w := get(h, tc.query, "ai-food", secret(1))
		if w.Code != 400 || reason(t, w) != tc.want || feed.calls != before {
			t.Errorf("%s: %d %s calls=%d", name, w.Code, w.Body, feed.calls-before)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/purgedUsers", nil)
	r.SetBasicAuth("ai-food", secret(1))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 405 || w.Header().Get("Allow") != "GET" {
		t.Fatalf("POST: %d %v", w.Code, w.Header())
	}
}

func TestPurgeFeedAuthentication(t *testing.T) {
	feed := &fakeFeed{records: records(1)}
	h, _ := purgefeed.New(map[string][]string{"ai-food": {secret(1), secret(2)}}, feed.list)
	for name, creds := range map[string][2]string{
		"missing":      {"", ""},
		"wrong secret": {"ai-food", secret(3)},
		"wrong client": {"other", secret(1)},
	} {
		w := get(h, "", creds[0], creds[1])
		if w.Code != 401 || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") || reason(t, w) != "UNAUTHENTICATED/INVALID_CLIENT" || feed.calls != 0 {
			t.Fatalf("%s: %d %v %s", name, w.Code, w.Header(), w.Body)
		}
	}
	// 新旧秘密重叠轮换期间两者均可用。
	for _, s := range []string{secret(1), secret(2)} {
		if w := get(h, "", "ai-food", s); w.Code != 200 {
			t.Fatalf("rotation secret rejected: %d", w.Code)
		}
	}
	// Bearer 令牌不是服务凭据。
	r := httptest.NewRequest(http.MethodGet, "/v1/purgedUsers", nil)
	r.Header.Set("Authorization", "Bearer consumer-access-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("bearer accepted: %d", w.Code)
	}
}

func TestPurgeFeedNotConfiguredAndDependencyFailure(t *testing.T) {
	feed := &fakeFeed{records: records(1)}
	h, err := purgefeed.New(nil, feed.list)
	if err != nil {
		t.Fatal(err)
	}
	if w := get(h, "", "ai-food", secret(1)); w.Code != 503 || reason(t, w) != "UNAVAILABLE/PURGE_FEED_NOT_CONFIGURED" || feed.calls != 0 {
		t.Fatalf("not configured: %d %s", w.Code, w.Body)
	}
	failing := &fakeFeed{err: errors.New("db down: postgres://secret@host")}
	h, _ = purgefeed.New(map[string][]string{"ai-food": {secret(1)}}, failing.list)
	w := get(h, "", "ai-food", secret(1))
	if w.Code != 503 || reason(t, w) != "UNAVAILABLE/DEPENDENCY_UNAVAILABLE" || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("dependency failure: %d %s", w.Code, w.Body)
	}
	if _, err := purgefeed.New(map[string][]string{"ai-food": {"short"}}, feed.list); err == nil {
		t.Fatal("invalid clients accepted")
	}
	if _, err := purgefeed.New(map[string][]string{"ai-food": {secret(1)}}, nil); err == nil {
		t.Fatal("nil lister accepted")
	}
}

// 数据库恢复后序号回退：已保存的游标超前，返回 INVALID_PAGE_TOKEN 让业务方从头重拉。
func TestPurgeFeedCursorAheadAfterRestore(t *testing.T) {
	feed := &fakeFeed{records: records(2)}
	h, _ := purgefeed.New(map[string][]string{"ai-food": {secret(1)}}, feed.list)
	ahead := base64.RawURLEncoding.EncodeToString([]byte("v1:9"))
	if w := get(h, "?page_token="+ahead, "ai-food", secret(1)); w.Code != 400 || reason(t, w) != "INVALID_ARGUMENT/INVALID_PAGE_TOKEN" {
		t.Fatalf("cursor ahead: %d %s", w.Code, w.Body)
	}
	if w := get(h, "", "ai-food", secret(1)); w.Code != 200 || len(decode(t, w).PurgedUsers) != 2 {
		t.Fatalf("restart from the beginning: %d %s", w.Code, w.Body)
	}
}
