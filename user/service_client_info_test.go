package user_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

func clientMeta(info *user.ClientInfo) user.Meta { m := meta1; m.ClientInfo = info; return m }
func clientLogin(t *testing.T, f *fixture, info *user.ClientInfo) user.TokenResult {
	t.Helper()
	ctx := context.Background()
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.SignInWithCode(ctx, f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1)), dev1, clientMeta(info))
	if err != nil {
		t.Fatal(err)
	}
	return requireSignInTokens(t, r)
}
func clientSnapshot(t *testing.T, f *fixture, p user.Principal, kind enum.ClientInfoSnapshotKind) db.SessionClientInfo {
	t.Helper()
	r, err := f.repo.Q().GetSessionClientInfo(context.Background(), db.GetSessionClientInfoParams{UserID: p.UserID, SessionID: p.SessionID, SnapshotKind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSessionClientInfoSnapshots(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	initial := &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Android", BrowserName: "Chrome", BrowserVersion: "123", DeviceType: enum.DevicePhone}
	pair := clientLogin(t, f, initial)
	p := principalOf(t, f, pair)
	a := clientSnapshot(t, f, p, enum.ClientSnapshotInitial)
	b := clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
	if a.BrowserName == nil || *a.BrowserName != "Chrome" || !a.ObserveTime.Equal(b.ObserveTime) || !a.ObserveTime.Equal(*f.clock) {
		t.Fatal("initial/latest missing")
	}
	f.advance(time.Minute)
	out, err := f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{ClientType: enum.ClientNative, AppName: "Example", AppVersion: "1"}))
	if err != nil {
		t.Fatal(err)
	}
	b = clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
	if b.BrowserName != nil || b.OsName == nil || *b.OsName != "Android" || b.AppVersion == nil || *b.AppVersion != "1" {
		t.Fatalf("native merge: %+v", b)
	}
	f.advance(time.Minute)
	out, err = f.svc.Refresh(ctx, out.RefreshToken, clientMeta(&user.ClientInfo{AppVersion: "2"}))
	if err != nil {
		t.Fatal(err)
	}
	b = clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
	if *b.AppVersion != "2" {
		t.Fatal("upgrade lost")
	}
	f.advance(time.Minute)
	out, err = f.svc.Refresh(ctx, out.RefreshToken, clientMeta(&user.ClientInfo{AppName: "Other"}))
	if err != nil {
		t.Fatal(err)
	}
	b = clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
	if b.AppVersion != nil {
		t.Fatal("old software version retained")
	}
	for _, info := range []*user.ClientInfo{nil, {}, {BrowserVersion: "orphan"}} {
		f.advance(time.Minute)
		out, err = f.svc.Refresh(ctx, out.RefreshToken, clientMeta(info))
		if err != nil {
			t.Fatal(err)
		}
		if got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest); !reflect.DeepEqual(got, b) {
			t.Fatal("empty observation updated snapshot")
		}
	}
	if got := clientSnapshot(t, f, p, enum.ClientSnapshotInitial); !reflect.DeepEqual(got, a) {
		t.Fatal("initial changed")
	}
	current, err := f.svc.GetCurrentSession(ctx, p)
	if err != nil || current.InitialClientInfo == nil || current.ClientInfo == nil || current.ClientInfo.AppName != "Other" {
		t.Fatalf("detail: %+v %v", current, err)
	}
	list, err := f.svc.ListSessions(ctx, p.UserID, p.SessionID)
	if err != nil || len(list) != 1 || list[0].ClientInfo == nil || list[0].ClientInfo.AppName != "Other" {
		t.Fatalf("list: %+v %v", list, err)
	}
	mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID)
	mustExec(t, f, `UPDATE session SET authentication_identities_known=false WHERE id=$1`, p.SessionID)
	current, err = f.svc.GetCurrentSession(ctx, p)
	if err != nil || current.AuthenticationIdentitiesKnown || current.ClientInfo == nil {
		t.Fatalf("unknown metadata: %+v %v", current, err)
	}
}
func TestSessionClientInfoValidationBeforeConsumption(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bad := clientMeta(&user.ClientInfo{OSName: "bad\nname"})
	if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	cred := f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1))
	if _, err := f.svc.SignInWithCode(ctx, cred, dev1, bad); !errors.Is(err, user.ErrInvalidClientInfo) {
		t.Fatalf("invalid: %v", err)
	}
	r, err := f.svc.SignInWithCode(ctx, cred, dev1, meta1)
	if err != nil {
		t.Fatal("credential consumed", err)
	}
	pair := requireSignInTokens(t, r)
	p := principalOf(t, f, pair)
	if _, err = f.svc.Refresh(ctx, pair.RefreshToken, bad); !errors.Is(err, user.ErrInvalidClientInfo) {
		t.Fatal(err)
	}
	if _, err = f.svc.Refresh(ctx, pair.RefreshToken, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(61 * time.Second)
	if err = f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
		t.Fatal(err)
	}
	cred = f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, f.sent.code(phone1))
	if _, err = f.svc.Reauthenticate(ctx, p, cred, bad); !errors.Is(err, user.ErrInvalidClientInfo) {
		t.Fatal(err)
	}
	if _, err = f.svc.Reauthenticate(ctx, p, cred, clientMeta(&user.ClientInfo{OSName: "Linux"})); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.Q().GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: p.UserID, SessionID: p.SessionID, SnapshotKind: enum.ClientSnapshotInitial}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("legacy fake initial", err)
	}
	if got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest); got.OsName == nil || *got.OsName != "Linux" {
		t.Fatal(got)
	}
}
func TestPendingClientInfoSnapshots(t *testing.T) {
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing initial", true: "preserved initial"}[first], func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			var initial *user.ClientInfo
			if first {
				initial = &user.ClientInfo{BrowserName: "Chrome", BrowserVersion: "123"}
			}
			r, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:client-info@openid"}, dev1, clientMeta(initial))
			if err != nil {
				t.Fatal(err)
			}
			pending := r.Pending
			cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
			f.advance(time.Minute)
			bad := clientMeta(&user.ClientInfo{AppName: "bad\nname"})
			if _, err = f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, bad); !errors.Is(err, user.ErrInvalidClientInfo) {
				t.Fatal(err)
			}
			r, err = f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, clientMeta(&user.ClientInfo{BrowserName: "Safari"}))
			if err != nil {
				t.Fatal(err)
			}
			pair := requireSignInTokens(t, r)
			p := principalOf(t, f, pair)
			a, e := f.repo.Q().GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: p.UserID, SessionID: p.SessionID, SnapshotKind: enum.ClientSnapshotInitial})
			if first {
				if e != nil || *a.BrowserName != "Chrome" || !a.ObserveTime.Before(*f.clock) {
					t.Fatalf("initial %+v %v", a, e)
				}
			} else if !errors.Is(e, pgx.ErrNoRows) {
				t.Fatal("fabricated initial", e)
			}
			b := clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
			if b.BrowserName == nil || *b.BrowserName != "Safari" || b.BrowserVersion != nil {
				t.Fatal(b)
			}
			f.advance(time.Second)
			replay, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, clientMeta(&user.ClientInfo{BrowserName: "Edge"}))
			if err != nil || *replay.Tokens != pair {
				t.Fatal("replay", err)
			}
			if got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest); !reflect.DeepEqual(got, b) {
				t.Fatal("replay updated")
			}
		})
	}
}
func TestRefreshClientInfoGrace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := clientLogin(t, f, &user.ClientInfo{AppName: "App", AppVersion: "1"})
	p := principalOf(t, f, pair)
	f.advance(time.Second)
	next, err := f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{AppVersion: "2"}))
	if err != nil {
		t.Fatal(err)
	}
	before := clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
	retry, err := f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{AppVersion: "3"}))
	if err != nil || retry != next {
		t.Fatal(err)
	}
	if got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest); !reflect.DeepEqual(got, before) {
		t.Fatal("grace updated")
	}
	mustExec(t, f, `CREATE FUNCTION reject_client() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic snapshot failure'; END $$`)
	mustExec(t, f, `CREATE TRIGGER reject_client BEFORE UPDATE ON session_client_info FOR EACH ROW EXECUTE FUNCTION reject_client()`)
	if _, err = f.svc.Refresh(ctx, next.RefreshToken, clientMeta(&user.ClientInfo{AppVersion: "4"})); err == nil {
		t.Fatal("snapshot failure ignored")
	}
	mustExec(t, f, `DROP TRIGGER reject_client ON session_client_info`)
	if _, err = f.svc.Refresh(ctx, next.RefreshToken, meta1); err != nil {
		t.Fatal("rotation escaped rollback", err)
	}
	if got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest); !reflect.DeepEqual(got, before) {
		t.Fatal("failure persisted")
	}
}
func TestSessionContextCleanupAndPurge(t *testing.T) {
	t.Run("cleanup retention and children", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		pair := clientLogin(t, f, &user.ClientInfo{OSName: "Linux"})
		p := principalOf(t, f, pair)
		mustExec(t, f, `UPDATE session SET revoke_time=$2 WHERE id=$1`, p.SessionID, f.clock.Add(-user.SessionRetention+time.Second))
		if n, err := f.svc.CleanupSessions(ctx); err != nil || n != 0 {
			t.Fatalf("retention %d %v", n, err)
		}
		mustExec(t, f, `UPDATE session SET revoke_time=$2 WHERE id=$1`, p.SessionID, f.clock.Add(-user.SessionRetention-time.Second))
		if n, err := f.svc.CleanupSessions(ctx); err != nil || n != 1 {
			t.Fatalf("cleanup %d %v", n, err)
		}
		for _, table := range []string{"session", "session_client_info", "session_authentication_identity"} {
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
				t.Fatalf("orphan %s %d %v", table, n, err)
			}
		}
	})
	t.Run("purge rollback and historical sessions", func(t *testing.T) {
		f := newFixture(t)
		ctx := context.Background()
		pair := clientLogin(t, f, &user.ClientInfo{OSName: "Linux"})
		p := principalOf(t, f, pair)
		mustExec(t, f, `UPDATE session SET revoke_time=$2 WHERE id=$1`, p.SessionID, *f.clock)
		mustExec(t, f, `UPDATE user_account SET state=$2,delete_time=$3,purge_time=$3 WHERE id=$1`, p.UserID, enum.UserPendingDeletion, *f.clock)
		sentinel := errors.New("synthetic callback")
		newServiceWith(t, f, func(d *user.Deps) {
			d.Anonymizers = []anonymize.Anonymizer{&auditPrivacyCallback{run: func(context.Context, pgx.Tx) error { return sentinel }}}
		})
		if n, err := f.svc.PurgeDueUsers(ctx); n != 0 || !errors.Is(err, sentinel) {
			t.Fatal(n, err)
		}
		clientSnapshot(t, f, p, enum.ClientSnapshotInitial)
		clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
		newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = nil })
		if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		for _, table := range []string{"session_client_info", "session_authentication_identity"} {
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
				t.Fatalf("purge orphan %s %d %v", table, n, err)
			}
		}
	})
}

// Remove new fields from real encrypted wire data to exercise a pre-upgrade ticket.
func stripPendingClientInfo(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	var id string
	var ct []byte
	var version uint16
	if err := f.pool.QueryRow(ctx, `SELECT id,payload,cipher_version FROM sign_in_completion_flow`).Scan(&id, &ct, &version); err != nil {
		t.Fatal(err)
	}
	searchPath, _, _ := strings.Cut(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")
	searchPath = strings.Trim(strings.TrimSpace(searchPath), `"`)
	extra := f.deps.SignInCompletionFlowContext
	if extra == "" {
		extra = f.deps.IdentityFlowContext
	}
	wire, _ := json.Marshal(struct{ SearchPath, Prefix, JWT, Extra string }{searchPath, f.deps.Codes.InstancePrefix(), f.deps.Signer.ContextFingerprint(), extra})
	hash := sha256.Sum256(wire)
	aad, _ := json.Marshal([]string{"accountkit-sign-in-completion-v1", fmt.Sprintf("%x", hash), id})
	plain, err := f.ciph.DecryptWithAAD(ct, version, aad)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err = json.Unmarshal([]byte(plain), &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "InitialClientInfo")
	delete(payload, "InitialClientInfoObserveTime")
	b, _ := json.Marshal(payload)
	ct, version, err = f.ciph.EncryptWithAAD(string(b), aad)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `UPDATE sign_in_completion_flow SET payload=$1,cipher_version=$2`, ct, version)
}
func TestPendingClientInfoLegacyAndFallback(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			firstAt := *f.clock
			r, err := f.svc.SignInWithIdp(ctx, user.IdpCredential{Kind: enum.IdentityWeChat, AppID: "wx1", Code: "union:legacy-client@openid"}, dev1, clientMeta(&user.ClientInfo{ClientType: enum.ClientWeb, BrowserName: "Chrome", BrowserVersion: "123"}))
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				stripPendingClientInfo(t, f)
			}
			cred := completionCredential(t, f, r.Pending, enum.IdentityEmail, email1)
			f.advance(time.Minute)
			info := (*user.ClientInfo)(nil)
			if legacy {
				info = &user.ClientInfo{OSName: "Linux"}
			}
			r, err = f.svc.CompleteSignIn(ctx, r.Pending.SignInCompletionToken, cred, user.Device{}, clientMeta(info))
			if err != nil {
				t.Fatal(err)
			}
			p := principalOf(t, f, requireSignInTokens(t, r))
			current, err := f.svc.GetCurrentSession(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if current.DeviceID != dev1.ID || current.DeviceName != dev1.Name {
				t.Fatal("pending device contract lost")
			}
			if legacy {
				if current.InitialClientInfo != nil || current.ClientInfo == nil || current.ClientInfo.OSName != "Linux" {
					t.Fatalf("legacy: %+v", current)
				}
			} else {
				if current.InitialClientInfo == nil || current.ClientInfo == nil || !reflect.DeepEqual(current.InitialClientInfo, current.ClientInfo) || !current.ClientInfoUpdateTime.Equal(firstAt) {
					t.Fatalf("fallback: %+v", current)
				}
			}
		})
	}
}
func TestSessionClientInfoWriteRollback(t *testing.T) {
	for _, operation := range []string{"login", "completion", "reauth"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			var p user.Principal
			var initial db.SessionClientInfo
			var run func() error
			switch operation {
			case "login":
				if err := f.sendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
					t.Fatal(err)
				}
				cred := f.credential(enum.PurposeSignIn, enum.IdentityPhone, phone1, f.sent.code(phone1))
				run = func() error {
					_, err := f.svc.SignInWithCode(ctx, cred, dev1, clientMeta(&user.ClientInfo{OSName: "Linux"}))
					return err
				}
			case "completion":
				pending := pendingFlow(t, f, "client-rollback")
				cred := completionCredential(t, f, pending, enum.IdentityEmail, email1)
				run = func() error {
					_, err := f.svc.CompleteSignIn(ctx, pending.SignInCompletionToken, cred, user.Device{}, clientMeta(&user.ClientInfo{OSName: "Linux"}))
					return err
				}
			case "reauth":
				pair := clientLogin(t, f, &user.ClientInfo{OSName: "Linux"})
				p = principalOf(t, f, pair)
				initial = clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
				f.advance(time.Minute)
				if err := f.sendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
					t.Fatal(err)
				}
				cred := f.credential(enum.PurposeReauth, enum.IdentityPhone, phone1, f.sent.code(phone1))
				run = func() error {
					_, err := f.svc.Reauthenticate(ctx, p, cred, clientMeta(&user.ClientInfo{OSName: "Android"}))
					return err
				}
			}
			mustExec(t, f, `CREATE FUNCTION reject_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic snapshot rollback'; END $$`)
			mustExec(t, f, `CREATE TRIGGER reject_snapshot BEFORE INSERT OR UPDATE ON session_client_info FOR EACH ROW EXECUTE FUNCTION reject_snapshot()`)
			if err := run(); err == nil {
				t.Fatal("snapshot write error ignored")
			}
			if operation == "reauth" {
				got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
				if !reflect.DeepEqual(got, initial) {
					t.Fatal("reauth snapshot escaped rollback")
				}
				current, err := f.svc.GetCurrentSession(ctx, p)
				if err != nil || !current.AuthTime.Equal(p.AuthTime) || !current.AuthenticationIdentitiesKnown || len(current.AuthenticationIdentities) != 1 {
					t.Fatal("partial reauth context", err)
				}
			} else {
				for _, table := range []string{"session", "session_client_info", "session_authentication_identity"} {
					var n int
					if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
						t.Fatalf("partial login %s %d %v", table, n, err)
					}
				}
			}
		})
	}
}
func TestSessionContextCleanupBoundedAndAtomic(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := clientLogin(t, f, &user.ClientInfo{OSName: "Linux"})
	p := principalOf(t, f, pair)
	// Copy a synthetic session batch and both child kinds, keeping one original.
	mustExec(t, f, `INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time)
 SELECT 's_'||lpad(n::text,13,'0'),$1,'synthetic-'||n,now(),decode(md5(n::text),'hex'),$2 FROM generate_series(1,5)n`, p.UserID, f.clock.Add(-user.SessionRetention-time.Second))
	mustExec(t, f, `INSERT INTO session_authentication_identity SELECT s.id,a.identity_id FROM session s CROSS JOIN session_authentication_identity a WHERE s.id<>$1 AND a.session_id=$1`, p.SessionID)
	mustExec(t, f, `INSERT INTO session_client_info(session_id,snapshot_kind,os_name,observe_time) SELECT s.id,c.snapshot_kind,c.os_name,c.observe_time FROM session s CROSS JOIN session_client_info c WHERE s.id<>$1 AND c.session_id=$1`, p.SessionID)
	mustExec(t, f, `CREATE FUNCTION reject_session_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic cleanup rollback'; END $$`)
	mustExec(t, f, `CREATE TRIGGER reject_session_delete BEFORE DELETE ON session FOR EACH ROW EXECUTE FUNCTION reject_session_delete()`)
	if _, err := f.svc.CleanupSessions(ctx); err == nil {
		t.Fatal("cleanup error ignored")
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_client_info`).Scan(&n); err != nil || n != 12 {
		t.Fatalf("children escaped rollback %d %v", n, err)
	}
	mustExec(t, f, `DROP TRIGGER reject_session_delete ON session`)
	if n, err := f.repo.Q().DeleteStaleSessions(ctx, db.DeleteStaleSessionsParams{Before: f.clock.Add(-user.SessionRetention), BatchSize: 2}); err != nil || n != 2 {
		t.Fatalf("bounded batch %d %v", n, err)
	}
	if n, err := f.svc.CleanupSessions(ctx); err != nil || n != 3 {
		t.Fatalf("remaining cleanup %d %v", n, err)
	}
	for _, table := range []string{"session_authentication_identity", "session_client_info"} {
		var n int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" c WHERE NOT EXISTS (SELECT 1 FROM session s WHERE s.id=c.session_id)").Scan(&n); err != nil || n != 0 {
			t.Fatalf("orphan %s %d %v", table, n, err)
		}
	}
	clientSnapshot(t, f, p, enum.ClientSnapshotInitial)
}
func TestSessionClientInfoCurrentAtomicRead(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pair := clientLogin(t, f, &user.ClientInfo{OSName: "Linux"})
	p := principalOf(t, f, pair)
	tx := identityRaceLock(t, ctx, f, p.UserID, p.SessionID)
	at := f.clock.Add(time.Minute)
	if _, err := tx.Exec(ctx, `UPDATE session SET auth_time=$2 WHERE id=$1`, p.SessionID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE session_client_info SET os_name='Android',observe_time=$2 WHERE session_id=$1 AND snapshot_kind=2`, p.SessionID, at); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	info := make(chan user.CurrentSessionInfo, 1)
	go func() { r, err := f.svc.GetCurrentSession(ctx, p); info <- r; done <- err }()
	waitIdentityRaceBlocked(t, ctx, f, tx, done)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r := <-info
	if r.ClientInfo.OSName != "Android" || !r.AuthTime.Equal(at) || !r.ClientInfoUpdateTime.Equal(at) || r.InitialClientInfo.OSName != "Linux" {
		t.Fatalf("mixed snapshot %+v", r)
	}
}

func TestSessionContextPurgeRefreshConcurrency(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pair := clientLogin(t, f, nil)
	p := principalOf(t, f, pair)
	barrier, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Rollback(context.Background())
	key := barrier.Conn().PgConn().PID()
	if _, err = barrier.Exec(ctx, `SELECT pg_advisory_xact_lock(716005,$1::int)`, key); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, fmt.Sprintf(`CREATE FUNCTION pause_client_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(716005,%d); RETURN NEW; END $$`, key))
	mustExec(t, f, `CREATE TRIGGER pause_client_refresh BEFORE INSERT OR UPDATE ON session_client_info FOR EACH ROW EXECUTE FUNCTION pause_client_refresh()`)
	refreshed := make(chan error, 1)
	go func() {
		_, err := f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{AppName: "App", AppVersion: "2"}))
		refreshed <- err
	}()
	waitIdentityRaceBlocked(t, ctx, f, barrier, refreshed)
	// Refresh has already observed ACTIVE and now holds the session row lock.
	// A concurrent deletion transition does not acquire that row lock.
	mustExec(t, f, `UPDATE user_account SET state=$2,delete_time=$3,purge_time=$3 WHERE id=$1`, p.UserID, enum.UserPendingDeletion, *f.clock)
	purged := make(chan error, 1)
	go func() {
		n, err := f.svc.PurgeDueUsers(ctx)
		if err == nil && n != 1 {
			err = fmt.Errorf("purge count %d", n)
		}
		purged <- err
	}()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Observe a chain barrier -> refresh -> purge, rather than guessing timing.
		var blocked bool
		err = f.pool.QueryRow(ctx, `SELECT EXISTS (
   SELECT 1 FROM pg_stat_activity refreshing,pg_stat_activity purging
   WHERE $1::int=ANY(pg_blocking_pids(refreshing.pid))
    AND refreshing.pid=ANY(pg_blocking_pids(purging.pid)))`, key).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-purged:
			t.Fatalf("purge did not serialize with refresh: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	// No snapshot existed before this in-flight first observation.
	if err = barrier.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-refreshed; err != nil {
		t.Fatal(err)
	}
	if err = <-purged; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"session_authentication_identity", "session_client_info"} {
		var n int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("refresh recreated purged %s %d %v", table, n, err)
		}
	}
	if _, err = f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{AppVersion: "3"})); !errors.Is(err, user.ErrInvalidGrant) {
		t.Fatalf("purged refresh accepted: %v", err)
	}
}

func TestSessionClientInfoSparseObservations(t *testing.T) {
	for _, initial := range []*user.ClientInfo{nil, {OSVersion: "1", BrowserVersion: "2", AppVersion: "3"}} {
		t.Run(fmt.Sprintf("%v", initial), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			pair := clientLogin(t, f, initial)
			p := principalOf(t, f, pair)
			mustExec(t, f, `DELETE FROM session_authentication_identity WHERE session_id=$1`, p.SessionID)
			mustExec(t, f, `UPDATE session SET authentication_identities_known=false WHERE id=$1`, p.SessionID)
			for _, kind := range []enum.ClientInfoSnapshotKind{enum.ClientSnapshotInitial, enum.ClientSnapshotLatest} {
				if _, err := f.repo.Q().GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: p.UserID, SessionID: p.SessionID, SnapshotKind: kind}); !errors.Is(err, pgx.ErrNoRows) {
					t.Fatal("fabricated software snapshot", err)
				}
			}
			out, err := f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{OSName: "Linux"}))
			if err != nil {
				t.Fatal(err)
			}
			current, err := f.svc.GetCurrentSession(ctx, p)
			if err != nil || current.InitialClientInfo != nil || current.ClientInfo == nil || current.ClientInfo.OSName != "Linux" || current.ClientInfo.OSVersion != "" || current.AuthenticationIdentitiesKnown {
				t.Fatalf("legacy refresh %+v %v", current, err)
			}
			before := clientSnapshot(t, f, p, enum.ClientSnapshotLatest)
			f.advance(time.Second)
			if _, err = f.svc.Refresh(ctx, out.RefreshToken, clientMeta(&user.ClientInfo{BrowserVersion: "orphan"})); err != nil {
				t.Fatal(err)
			}
			if got := clientSnapshot(t, f, p, enum.ClientSnapshotLatest); !reflect.DeepEqual(got, before) {
				t.Fatal("orphan-only observation advanced time")
			}
		})
	}
}
