package migrations

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

func TestSessionContextMigrationUpgrade(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	ctx := context.Background()
	seedRenameFlow(t, c, "sign_in_completion_flow")
	original := func() string {
		t.Helper()
		var data string
		if err := c.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(to_jsonb(t)-'authentication_identities_known' ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t))::text`).Scan(&data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	originalData := original()
	var missing bool
	if err := c.QueryRow(ctx, `SELECT to_regclass('session_authentication_identity') IS NULL AND to_regclass('session_client_info') IS NULL AND NOT EXISTS(SELECT FROM information_schema.columns WHERE table_schema=$1 AND table_name='session' AND column_name='authentication_identities_known')`, schema).Scan(&missing); err != nil || !missing {
		t.Fatalf("v1 already has context: %v %v", missing, err)
	}
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	var known bool
	var hash string
	if err := c.QueryRow(ctx, `SELECT authentication_identities_known,encode(refresh_token_hash,'hex') FROM session`).Scan(&known, &hash); err != nil {
		t.Fatal(err)
	}
	if known || hash != "1234" {
		t.Fatalf("upgrade changed credentials/context: %v %s", known, hash)
	}
	var rows int
	if err := c.QueryRow(ctx, `SELECT (SELECT count(*) FROM session_authentication_identity)+(SELECT count(*) FROM session_client_info)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("fabricated context: %d %v", rows, err)
	}
	if original() != originalData {
		t.Fatal("upgrade changed preexisting rows, including credential bytes")
	}
	assertVersion(t, cfg, schema, 2, false)
	for _, sql := range []string{
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001')`,
		`INSERT INTO session_client_info(session_id,snapshot_kind,os_name,observe_time) VALUES('s_0000000000001',1,'Synthetic OS',now())`,
	} {
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	before, catalog := renameDataSnapshot(t, c, "sign_in_completion_flow"), migrationCatalog(t, c, schema)
	for range 2 {
		if err := Up(ctx, cfg, schema); err != nil {
			t.Fatal(err)
		}
	}
	if before != renameDataSnapshot(t, c, "sign_in_completion_flow") || catalog != migrationCatalog(t, c, schema) {
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
			`INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001',3,'synthetic-a'),('i_0000000000002','u_0000000000001',4,'synthetic-b'),('i_0000000000003','u_0000000000002',3,'synthetic-c')`,
			`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) SELECT 's_000000000000'||n,CASE WHEN n=5 THEN 'u_0000000000002' ELSE 'u_0000000000001' END,'device',now(),decode('000'||n,'hex'),CASE WHEN n=3 THEN now()-interval '1 day' ELSE now()+interval '1 day' END FROM generate_series(1,5) n`,
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
	for _, sid := range []string{"s_0000000000001", "s_0000000000002", "s_0000000000003"} {
		iid := "i_0000000000001"
		if sid == "s_0000000000002" {
			iid = "i_0000000000002"
		}
		exec("AddSessionAuthenticationIdentity", pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": sid, "identity_id": iid})
		exec("MarkSessionAuthenticationIdentitiesKnown", pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": sid, "now": now})
	}
	rows, err := c.Query(ctx, contextQuery(t, "ListSessionAuthenticationIdentities"), args)
	if err != nil {
		t.Fatal(err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		IdentityID string
		Kind       int16
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
	snapshot := pgx.NamedArgs{"user_id": "u_0000000000001", "session_id": "s_0000000000001", "snapshot_kind": int16(2), "client_type": int16(1), "device_type": nil, "os_name": "Synthetic OS", "os_version": nil, "browser_name": nil, "browser_version": nil, "app_name": nil, "app_version": nil, "device_model": nil, "observe_time": now, "now": now}
	if exec("UpsertSessionClientInfo", snapshot) != 1 {
		t.Fatal("snapshot not inserted")
	}
	snapshot["user_id"] = "u_0000000000002"
	if exec("UpsertSessionClientInfo", snapshot) != 0 {
		t.Fatal("cross-user snapshot upsert")
	}
	var sid string
	err = c.QueryRow(ctx, contextQuery(t, "GetSessionClientInfo"), snapshot).Scan(new(string), new(int16), new(*int16), new(*int16), new(*string), new(*string), new(*string), new(*string), new(*string), new(*string), new(*string), new(time.Time), new(time.Time), new(time.Time))
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-user read: %v", err)
	}
	rows, err = c.Query(ctx, contextQuery(t, "ListSessionClientInfosByUser"), pgx.NamedArgs{"user_id": "u_0000000000001", "session_ids": []string{"s_0000000000001", "s_0000000000005"}, "snapshot_kind": int16(2)})
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
	if err != nil || len(revoked) != 3 {
		t.Fatalf("revoke must include match/expired/unknown only: %v %v", revoked, err)
	}
	if err := c.QueryRow(ctx, `SELECT id FROM session WHERE id='s_0000000000002' AND revoke_time IS NULL`).Scan(&sid); err != nil {
		t.Fatal("unrelated source revoked", err)
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
		`INSERT INTO session_client_info(session_id,snapshot_kind,client_type,device_type,observe_time) VALUES('s_0000000000001',99,99,99,now())`,
	} {
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatal("unexpected FK or enum CHECK", err)
		}
	}
	for _, sql := range []string{
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001')`,
		`INSERT INTO session_authentication_identity VALUES('invalid','i_0000000000001')`,
		`INSERT INTO session_authentication_identity VALUES('s_0000000000001','invalid')`,
		`INSERT INTO session_client_info(session_id,snapshot_kind,observe_time) VALUES('invalid',1,now())`,
		`INSERT INTO session_client_info(session_id,snapshot_kind,observe_time) VALUES('s_0000000000001',99,now())`,
		`INSERT INTO session_client_info(session_id,snapshot_kind,os_version,observe_time) VALUES('s_0000000000001',1,repeat('x',33),now())`,
	} {
		if _, err := c.Exec(ctx, sql); err == nil {
			t.Fatal("missing format/unique/length constraint", sql)
		}
	}
	seedRenameFlow(t, c, "sign_in_completion_flow")
	q := db.New(c)
	if n, err := q.AddSessionAuthenticationIdentity(ctx, db.AddSessionAuthenticationIdentityParams{UserID: "u_0000000000001", SessionID: "s_0000000000001", IdentityID: "i_0000000000001"}); err != nil || n != 0 {
		t.Fatalf("generated duplicate write %d %v", n, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	osName := "Synthetic OS"
	arg := db.UpsertSessionClientInfoParams{UserID: "u_0000000000001", SessionID: "s_0000000000001", SnapshotKind: enum.ClientSnapshotInitial, OsName: &osName, ObserveTime: now, Now: now}
	if n, err := q.UpsertSessionClientInfo(ctx, arg); err != nil || n != 1 {
		t.Fatalf("generated snapshot insert %d %v", n, err)
	}
	next := "Other OS"
	arg.OsName = &next
	arg.Now = now.Add(time.Hour)
	arg.ObserveTime = arg.Now
	if n, err := q.UpsertSessionClientInfo(ctx, arg); err != nil || n != 0 {
		t.Fatalf("INITIAL was mutable %d %v", n, err)
	}
	info, err := q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: arg.UserID, SessionID: arg.SessionID, SnapshotKind: arg.SnapshotKind})
	if err != nil || info.ClientType != nil || info.DeviceType != nil || info.OsName == nil || *info.OsName != osName || !info.ObserveTime.Equal(now) || !info.CreateTime.Equal(now) || !info.UpdateTime.Equal(now) {
		t.Fatalf("nullable enum/initial snapshot roundtrip: %+v %v", info, err)
	}
	arg.SnapshotKind = enum.ClientSnapshotLatest
	web := enum.ClientWeb
	phone := enum.DevicePhone
	arg.ClientType = &web
	arg.DeviceType = &phone
	if _, err := q.UpsertSessionClientInfo(ctx, arg); err != nil {
		t.Fatal(err)
	}
	info, err = q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: arg.UserID, SessionID: arg.SessionID, SnapshotKind: arg.SnapshotKind})
	if err != nil || info.ClientType == nil || *info.ClientType != web || info.DeviceType == nil || *info.DeviceType != phone {
		t.Fatalf("enum roundtrip: %+v %v", info, err)
	}
}

func TestSessionContextBaselineRejectsPartialVersion(t *testing.T) {
	for _, mutation := range []string{
		`ALTER TABLE session DROP COLUMN authentication_identities_known`,
		`ALTER TABLE session ALTER COLUMN authentication_identities_known SET DEFAULT true`,
		`DROP TABLE session_authentication_identity`,
		`ALTER TABLE session_client_info DROP COLUMN device_type`,
		`ALTER TABLE session_client_info ALTER COLUMN client_type SET NOT NULL`,
		`ALTER TABLE session_client_info ALTER COLUMN os_version TYPE varchar(64)`,
		`ALTER TABLE session_client_info DROP CONSTRAINT session_client_info_pkey`,
		`ALTER TABLE session_authentication_identity DROP CONSTRAINT session_authentication_identity_identity_id_format`,
		`DROP INDEX idx_session_authentication_identity_identity_id_session_id`,
		`DROP INDEX idx_session_user_id_id`,
	} {
		t.Run(strings.Fields(mutation)[2]+"/"+mutation, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			ctx := context.Background()
			if err := Up(ctx, cfg, schema); err != nil {
				t.Fatal(err)
			}
			c := migrationTestConn(t, cfg, schema)
			seedRenameFlow(t, c, "sign_in_completion_flow")
			if _, err := c.Exec(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			before, catalog := renameDataSnapshot(t, c, "sign_in_completion_flow"), migrationCatalog(t, c, schema)
			if err := Up(ctx, cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("incomplete v2 accepted: %v", err)
			}
			assertVersion(t, cfg, schema, 2, false)
			if before != renameDataSnapshot(t, c, "sign_in_completion_flow") || catalog != migrationCatalog(t, c, schema) {
				t.Fatal("rejection changed data/catalog")
			}
		})
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
	seedRenameFlow(t, c, "sign_in_completion_flow")
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

func TestSessionContextPrecheckIgnoresConcurrentOtherSchemaDDL(t *testing.T) {
	cfg, schema := safetyDB(t)
	_, otherSchema := safetyDB(t)
	ctx := context.Background()
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["join_collapse_limit"] = "1"
	cfg.RuntimeParams["from_collapse_limit"] = "1"
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	other := migrationTestConn(t, cfg, otherSchema)
	if _, err := other.Exec(ctx, "CREATE SCHEMA "+otherSchema); err != nil {
		t.Fatal(err)
	}
	churn, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	errs := make(chan error, 1)
	go func() {
		defer wg.Done()
		for churn.Err() == nil {
			_, err := other.Exec(ctx, `CREATE TABLE session_authentication_identity(session_id text CONSTRAINT session_authentication_identity_session_id_format CHECK(session_id ~ '^s_[0-9a-hjkmnp-tv-z]{13}$'),identity_id text CONSTRAINT session_authentication_identity_identity_id_format CHECK(identity_id ~ '^i_[0-9a-hjkmnp-tv-z]{13}$'),CONSTRAINT session_authentication_identity_pkey PRIMARY KEY(session_id,identity_id)); CREATE INDEX idx_session_authentication_identity_identity_id_session_id ON session_authentication_identity(identity_id,session_id); CREATE TABLE session(user_id text,id text); CREATE INDEX idx_session_user_id_id ON session(user_id,id); CREATE TABLE session_client_info(session_id text CONSTRAINT session_client_info_session_id_format CHECK(session_id ~ '^s_[0-9a-hjkmnp-tv-z]{13}$'),snapshot_kind smallint,CONSTRAINT session_client_info_pkey PRIMARY KEY(session_id,snapshot_kind)); `)
			if err == nil {
				_, err = other.Exec(ctx, `DROP TABLE session_authentication_identity; DROP TABLE session; DROP TABLE session_client_info`)
			}
			if err != nil {
				errs <- err
				return
			}
		}
	}()
	defer func() {
		stop()
		wg.Wait()
		select {
		case err := <-errs:
			t.Error(err)
		default:
		}
	}()
	for range 40 {
		if err := Up(ctx, cfg, schema); err != nil {
			t.Fatal("other schema DDL broke precheck", err)
		}
	}
}
