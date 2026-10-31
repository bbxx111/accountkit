package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/jackc/pgx/v5"
)

func TestSessionContextInitialRepeat(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	ctx := context.Background()
	seedInitialFlow(t, c)
	original := func() string {
		t.Helper()
		var data string
		if err := c.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t),(SELECT jsonb_agg(t ORDER BY session_id,identity_id) FROM session_authentication_identity t),(SELECT jsonb_agg(t ORDER BY session_id) FROM session_client_info t))::text`).Scan(&data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	originalData := original()
	var complete bool
	if err := c.QueryRow(ctx, `SELECT to_regclass('session_authentication_identity') IS NOT NULL AND to_regclass('session_client_info') IS NOT NULL AND NOT EXISTS(SELECT FROM information_schema.columns WHERE table_schema=$1 AND table_name='session' AND column_name='authentication_identities_known')`, schema).Scan(&complete); err != nil || !complete {
		t.Fatalf("v1 lacks context: %v %v", complete, err)
	}
	initialUp(t, cfg, schema)
	var hash string
	if err := c.QueryRow(ctx, `SELECT encode(refresh_token_hash,'hex') FROM session`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "1234" {
		t.Fatalf("repeat changed credentials/context: %s", hash)
	}
	var rows int
	if err := c.QueryRow(ctx, `SELECT (SELECT count(*) FROM session_authentication_identity)+(SELECT count(*) FROM session_client_info)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("fabricated context: %d %v", rows, err)
	}
	if original() != originalData {
		t.Fatal("repeat changed preexisting rows, including credential bytes")
	}
	assertVersion(t, cfg, schema, 1, false)
	for _, sql := range []string{
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001')`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000001','{"os":{"name":"Synthetic OS"}}',now())`,
	} {
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	before, catalog := original(), migrationCatalog(t, c, schema)
	for range 2 {
		initialUp(t, cfg, schema)
	}
	if before != original() || catalog != migrationCatalog(t, c, schema) {
		t.Fatal("repeat Up changed existing data/catalog")
	}
	if err := c.QueryRow(ctx, `SELECT count(*) FROM session_authentication_identity`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("repeat lost association: %d %v", rows, err)
	}
	if err := c.QueryRow(ctx, `SELECT count(*) FROM session_client_info`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("repeat lost snapshot: %d %v", rows, err)
	}
}

// Load the exact source query before generation as well as afterwards. pgx named
// arguments preserve its parameter binding; the generator consistency gate runs separately.
func contextQuery(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../user/query.sql")
	if err != nil {
		t.Fatal(err)
	}
	marker := "-- name: " + name + " :"
	_, tail, ok := strings.Cut(string(b), marker)
	if !ok {
		t.Fatalf("missing query %s", name)
	}
	_, tail, _ = strings.Cut(tail, "\n")
	sql, _, _ := strings.Cut(tail, "-- name:")
	return regexp.MustCompile(`sqlc\.(?:narg|arg)\('([a-z_]+)'\)`).ReplaceAllString(sql, "@$1")
}

func TestSessionContextQueriesIsolation(t *testing.T) {
	cfg, a := safetyDB(t)
	_, b := safetyDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, schema := range []string{a, b} {
		if err := Up(ctx, cfg, schema); err != nil {
			t.Fatal(err)
		}
	}
	c := migrationTestConn(t, cfg, a)
	other := migrationTestConn(t, cfg, b)
	for _, conn := range []*pgx.Conn{c, other} {
		for _, sql := range []string{
			`INSERT INTO user_account(id,state) VALUES('u_0000000000001',1),('u_0000000000002',1)`,
			`INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001','WECHAT','synthetic-a'),('i_0000000000002','u_0000000000001','APPLE','synthetic-b'),('i_0000000000003','u_0000000000002','WECHAT','synthetic-c')`,
			`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) SELECT 's_000000000000'||n,CASE WHEN n=5 THEN 'u_0000000000002' ELSE 'u_0000000000001' END,'device-'||n,now(),decode('000'||n,'hex'),CASE WHEN n=3 THEN now()-interval '1 day' ELSE now()+interval '1 day' END FROM generate_series(1,5) n`,
		} {
			if _, err := conn.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec := func(name string, args pgx.NamedArgs) int64 {
		t.Helper()
		tag, err := c.Exec(ctx, contextQuery(t, name), args)
		if err != nil {
			t.Fatal(name, err)
		}
		return tag.RowsAffected()
	}
	args := pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": "s_0000000000001", "identity_id": "i_0000000000001"}
	if exec("AddSessionAuthenticationIdentity", args) != 1 {
		t.Fatal("association not added")
	}
	if exec("AddSessionAuthenticationIdentity", args) != 0 {
		t.Fatal("duplicate association added")
	}
	bad := pgx.NamedArgs{"user_id": "u_0000000000002", "session_id": "s_0000000000001", "identity_id": "i_0000000000003"}
	if exec("AddSessionAuthenticationIdentity", bad) != 0 {
		t.Fatal("cross-user association added")
	}
	bad = pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": "s_0000000000001", "identity_id": "i_0000000000003"}
	if exec("AddSessionAuthenticationIdentity", bad) != 0 {
		t.Fatal("foreign identity added")
	}
	for _, sid := range []string{"s_0000000000001", "s_0000000000002", "s_0000000000003", "s_0000000000004"} {
		iid := "i_0000000000001"
		if sid == "s_0000000000002" || sid == "s_0000000000004" {
			iid = "i_0000000000002"
		}
		exec("AddSessionAuthenticationIdentity", pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": sid, "identity_id": iid})
	}
	rows, err := c.Query(ctx, contextQuery(t, "ListSessionAuthenticationIdentities"), args)
	if err != nil {
		t.Fatal(err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		IdentityID string
		Kind       string
	}])
	if err != nil || len(values) != 1 || values[0].IdentityID != "i_0000000000001" {
		t.Fatalf("identities: %v %v", values, err)
	}
	bad["user_id"] = "u_0000000000002"
	rows, err = c.Query(ctx, contextQuery(t, "ListSessionAuthenticationIdentities"), bad)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("cross-user source read")
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	// The caller's user scope also protects reads and writes for snapshots.
	snapshot := pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": "s_0000000000001", "client_info": []byte(`{"type":"WEB","os":{"name":"Synthetic OS"}}`), "observe_time": now, "now": now}
	if exec("UpsertSessionClientInfo", snapshot) != 1 {
		t.Fatal("snapshot not inserted")
	}
	snapshot["user_id"] = "u_0000000000002"
	if exec("UpsertSessionClientInfo", snapshot) != 0 {
		t.Fatal("cross-user snapshot upsert")
	}
	var sid string
	err = c.QueryRow(ctx, contextQuery(t, "GetSessionClientInfo"), snapshot).Scan(new(string), new([]byte), new(time.Time), new(time.Time), new(time.Time))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-user read: %v", err)
	}
	rows, err = c.Query(ctx, contextQuery(t, "ListSessionClientInfosByUser"), pgx.NamedArgs{"user_id": "u_0000000000001", "session_ids": []string{"s_0000000000001", "s_0000000000005"}})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	if rows.Err() != nil || count != 1 {
		t.Fatalf("batch read %d %v", count, rows.Err())
	}
	rows.Close()
	rows, err = c.Query(ctx, contextQuery(t, "RevokeSessionsByIdentity"), pgx.NamedArgs{"user_id": "u_0000000000001", "identity_id": "i_0000000000001", "now": now, "reason": int16(1)})
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	slices.Sort(revoked)
	if err != nil || !slices.Equal(revoked, []string{"s_0000000000001", "s_0000000000003"}) {
		t.Fatalf("revoke must include only exact matches, including expired: %v %v", revoked, err)
	}
	if err := c.QueryRow(ctx, `SELECT id FROM session WHERE id='s_0000000000002' AND revoke_time IS NULL`).Scan(&sid); err != nil {
		t.Fatal("unrelated source revoked", err)
	}
	if err := c.QueryRow(ctx, `SELECT id FROM session WHERE id='s_0000000000004' AND revoke_time IS NULL`).Scan(&sid); err != nil {
		t.Fatal("second unrelated source revoked", err)
	}
	if err := c.QueryRow(ctx, `SELECT id FROM session WHERE id='s_0000000000005' AND revoke_time IS NULL`).Scan(&sid); err != nil {
		t.Fatal("other user revoked", err)
	}
	exec("DeleteSessionAuthenticationIdentities", pgx.NamedArgs{"user_id": "u_0000000000002", "session_id": "s_0000000000001"})
	exec("DeleteSessionContextByUser", pgx.NamedArgs{"user_id": "u_0000000000002"})
	var n int
	if err := c.QueryRow(ctx, `SELECT count(*) FROM session_client_info`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("other user's cleanup affected snapshot %d %v", n, err)
	}
	exec("DeleteSessionContextByUser", pgx.NamedArgs{"user_id": "u_0000000000001"})
	if err := c.QueryRow(ctx, `SELECT (SELECT count(*) FROM session_client_info)+(SELECT count(*) FROM session_authentication_identity)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cleanup left context %d %v", n, err)
	}
	if err := other.QueryRow(ctx, `SELECT count(*) FROM session WHERE revoke_time IS NULL`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("other schema changed %d %v", n, err)
	}
}

func TestSessionContextStorageConstraints(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	// No foreign keys: structure accepts well-formed orphan references, while the
	// service-scoped writer separately refuses absent/cross-user targets.
	for _, sql := range []string{
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001')`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000002','{"type":"UNKNOWN","os":{"version":"this version exceeds the business limit"}}',now())`,
	} {
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatal("unexpected FK or JSON business CHECK", err)
		}
	}
	for _, sql := range []string{
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001')`,
		`INSERT INTO session_authentication_identity VALUES('invalid','i_0000000000001')`,
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','invalid')`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('invalid','{}',now())`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000002','{}',now())`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000003',NULL,now())`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time) VALUES('s_0000000000003','{}',NULL)`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time,create_time) VALUES('s_0000000000003','{}',now(),NULL)`,
		`INSERT INTO session_client_info(session_id,client_info,observe_time,update_time) VALUES('s_0000000000003','{}',now(),NULL)`,
	} {
		if _, err := c.Exec(ctx, sql); err == nil {
			t.Fatal("missing format/unique/not-null constraint", sql)
		}
	}
	seedInitialFlow(t, c)
	q := db.New(c)
	if n, err := q.AddSessionAuthenticationIdentity(ctx, db.AddSessionAuthenticationIdentityParams{UserID: "u_0000000000001", SessionID: "s_0000000000001", IdentityID: "i_0000000000001"}); err != nil || n != 0 {
		t.Fatalf("generated duplicate write %d %v", n, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	arg := db.UpsertSessionClientInfoParams{UserID: "u_0000000000001", SessionID: "s_0000000000001", ClientInfo: []byte(`{"type":"NATIVE","os":{"name":"Synthetic OS"},"app":{"name":"Synthetic App","version":"1"},"browser":{"name":"Synthetic Browser"},"extensions":{"release":{"stable":true,"counter":9007199254740993,"values":[null,1,"sample"]}}}`), ObserveTime: now, Now: now}
	assertJSON := func(got, want []byte) {
		t.Helper()
		decode := func(value []byte) any {
			decoder := json.NewDecoder(strings.NewReader(string(value)))
			decoder.UseNumber()
			var decoded any
			if err := decoder.Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			return decoded
		}
		if !reflect.DeepEqual(decode(got), decode(want)) {
			t.Fatalf("JSON roundtrip changed content: got %s, want %s", got, want)
		}
	}
	// Generated writes cannot attach data to a missing or foreign session.
	bad := arg
	bad.SessionID = "s_0000000000003"
	if n, err := q.UpsertSessionClientInfo(ctx, bad); err != nil || n != 0 {
		t.Fatalf("absent session accepted: %d %v", n, err)
	}
	bad = arg
	bad.UserID = "u_0000000000002"
	if n, err := q.UpsertSessionClientInfo(ctx, bad); err != nil || n != 0 {
		t.Fatalf("foreign session accepted: %d %v", n, err)
	}
	if n, err := q.UpsertSessionClientInfo(ctx, arg); err != nil || n != 1 {
		t.Fatalf("generated description insert %d %v", n, err)
	}
	info, err := q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: arg.UserID, SessionID: arg.SessionID})
	if err != nil || info.SessionID != arg.SessionID || !info.ObserveTime.Equal(now) || !info.CreateTime.Equal(now) || !info.UpdateTime.Equal(now) {
		t.Fatalf("description metadata roundtrip: %+v %v", info, err)
	}
	assertJSON(info.ClientInfo, arg.ClientInfo)
	arg.ClientInfo = []byte(`{"browser":{"name":"Other Browser"}}`)
	arg.Now = now.Add(time.Hour)
	arg.ObserveTime = arg.Now
	if n, err := q.UpsertSessionClientInfo(ctx, arg); err != nil || n != 1 {
		t.Fatalf("single description replacement %d %v", n, err)
	}
	info, err = q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: arg.UserID, SessionID: arg.SessionID})
	if err != nil || !info.ObserveTime.Equal(arg.Now) || !info.CreateTime.Equal(now) || !info.UpdateTime.Equal(arg.Now) {
		t.Fatalf("replacement metadata: %+v %v", info, err)
	}
	assertJSON(info.ClientInfo, arg.ClientInfo)
	var count int
	if err := c.QueryRow(ctx, `SELECT count(*) FROM session_client_info WHERE session_id=$1`, arg.SessionID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replacement created extra rows: %d %v", count, err)
	}
}

func TestSessionContextReadLock(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	writer := migrationTestConn(t, cfg, schema)
	seedInitialFlow(t, c)
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if _, err := q.LockCurrentSessionForRead(ctx, db.LockCurrentSessionForReadParams{UserID: "u_0000000000002", SessionID: "s_0000000000001"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-user read lock: %v", err)
	}
	if _, err := q.LockCurrentSessionForRead(ctx, db.LockCurrentSessionForReadParams{UserID: "u_0000000000001", SessionID: "s_0000000000001"}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := writer.Exec(wait, `UPDATE session SET auth_time=now() WHERE id='s_0000000000001'`); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writer bypassed consistent-read lock: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSessionContextIdentityLookupPlan(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	if _, err := c.Exec(ctx, `INSERT INTO session_authentication_identity(session_id,identity_id) SELECT 's_'||lpad(to_hex(n),13,'0'),'i_'||lpad(to_hex(n),13,'0') FROM generate_series(1,10000) n; ANALYZE session_authentication_identity`); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Query(ctx, `EXPLAIN (ANALYZE, FORMAT TEXT) SELECT session_id FROM session_authentication_identity WHERE identity_id='i_0000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if plan := strings.Join(lines, "\n"); !strings.Contains(plan, "idx_session_authentication_identity_identity_id_session_id") {
		t.Fatalf("identity lookup missed reverse index: %s", plan)
	}
}
