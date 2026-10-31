package migrations

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
)

func safetySource(t *testing.T, failure bool) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		b, err := fs.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = &fstest.MapFile{Data: b}
	}
	name := "testdata/0002_success.up.sql"
	if failure {
		name = "testdata/0002_failure.up.sql"
	}
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	out[fmt.Sprintf("%04d_probe.up.sql", latestVersion(t)+1)] = &fstest.MapFile{Data: b}
	out[fmt.Sprintf("%04d_probe.down.sql", latestVersion(t)+1)] = &fstest.MapFile{Data: []byte("DROP TABLE upgrade_probe")}
	if failure {
		out[fmt.Sprintf("%04d_after.up.sql", latestVersion(t)+2)] = &fstest.MapFile{Data: []byte("CREATE TABLE after_failure (id integer)")}
	}
	return out
}
func sourceUp(t *testing.T, cfg *pgx.ConnConfig, schema string, failure bool) error {
	t.Helper()
	return runWithSource(context.Background(), cfg, schema, safetySource(t, failure), func(m *migrate.Migrate) error { return m.Up() })
}
func TestUpgradePreservesDataAndIsRepeatable(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	const (
		uid       = "u_0000000000001"
		iid       = "i_0000000000001"
		sid       = "s_0000000000001"
		fid       = "f_0000000000001"
		subject   = "upgrade@example.test"
		refresh   = "synthetic-upgrade-refresh-credential"
		flowToken = "synthetic-upgrade-completion-credential"
		flowPlain = `{"kind":"EMAIL","target":"upgrade@example.test"}`
	)
	created := time.Date(2024, 3, 4, 5, 6, 7, 123456000, time.UTC)
	authTime := created.Add(time.Hour).Truncate(time.Second)
	observed := created.Add(2 * time.Hour)
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	cipher, err := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	digester, err := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{2}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	identityCiphertext, cipherVersion, err := cipher.Encrypt(subject)
	if err != nil {
		t.Fatal(err)
	}
	digest, digestVersion := digester.Digest(subject)
	// This is a storage-level authenticated payload, not a fabricated domain completion result.
	flowAAD := []byte("synthetic-upgrade-flow-context")
	flowCiphertext, flowVersion, err := cipher.EncryptWithAAD(flowPlain, flowAAD)
	if err != nil {
		t.Fatal(err)
	}
	refreshHash := sha256.Sum256([]byte(refresh))
	flowHash := sha256.Sum256([]byte(flowToken))
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_account(id,state,display_name,avatar_url,profile_attributes,create_time,update_time) VALUES($1,1,'Upgrade user','https://example.test/upgrade.png','{"counter":9007199254740993,"ratio":0.12345678901234567890123456789}',$2,$3)`, []any{uid, created, observed}},
		{`INSERT INTO user_account(id,state,delete_time,purge_time,create_time,update_time) VALUES('u_0000000000002',4,$1,$2,$1,$2)`, []any{created, observed}},
		{`INSERT INTO identity(id,user_id,kind,subject_digest,digest_key_version,subject_ciphertext,cipher_key_version,create_time,update_time) VALUES($1,$2,'EMAIL',$3,$4,$5,$6,$7,$7)`, []any{iid, uid, digest, int16(digestVersion), identityCiphertext, int16(cipherVersion), created}},
		{`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time,create_time,update_time,last_used_time) VALUES($1,$2,'upgrade-device',$3,$4,$5,$6,$6,$6)`, []any{sid, uid, authTime, refreshHash[:], deadline, created}},
		{`INSERT INTO session_authentication_identity(session_id,identity_id) VALUES($1,$2)`, []any{sid, iid}},
		{`INSERT INTO session_client_info(session_id,client_info,observe_time,create_time,update_time) VALUES($1,'{"type":"NATIVE","app":{"name":"Upgrade App","version":"1.0","build":"123"},"extensions":{"release":{"revision":9007199254740993}}}',$2,$3,$2)`, []any{sid, observed, created}},
		{`INSERT INTO audit_event(id,source,action,result,actor,targets,occur_time,create_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','SIGN_IN',1,'{"type":"USER","namespace":"urn:test:accounts","id":"u_0000000000001"}','[{"type":"IDENTITY_FLOW","namespace":"urn:test:accounts","id":"f_0000000000001"}]',$1,$2)`, []any{created, observed}},
		{`INSERT INTO sign_in_completion_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,proof_user_ids,create_time,update_time,expire_time) VALUES($1,$2,1,'policy',$3,$4,ARRAY[$5::text],$6,$6,$7)`, []any{fid, hex.EncodeToString(flowHash[:]), flowCiphertext, int16(flowVersion), uid, created, deadline}},
		{`INSERT INTO purged_user(user_id,purge_time) VALUES('u_0000000000002',$1)`, []any{observed}},
	} {
		if _, err := c.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	signer, err := tokens.NewSigner(tokens.Options{Keys: map[uint16]*ecdsa.PrivateKey{1: testkeys.JWT(1)}, Active: 1, Issuer: "upgrade-test", Audience: "upgrade-client"})
	if err != nil {
		t.Fatal(err)
	}
	access, _, err := signer.Sign(tokens.Claims{UserID: uid, SessionID: sid, Scope: "user", AuthTime: authTime}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var s string
		if err := c.QueryRow(ctx, `SELECT jsonb_build_array(
 (SELECT jsonb_agg(t ORDER BY id) FROM user_account t),
 (SELECT jsonb_agg(t ORDER BY id) FROM identity t),
 (SELECT jsonb_agg(t ORDER BY id) FROM session t),
 (SELECT jsonb_agg(t ORDER BY session_id,identity_id) FROM session_authentication_identity t),
 (SELECT jsonb_agg(t ORDER BY session_id) FROM session_client_info t),
 (SELECT jsonb_agg(t ORDER BY id) FROM audit_event t),
 (SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),
 (SELECT jsonb_agg(t ORDER BY seq,user_id) FROM purged_user t))::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	catalog := migrationCatalog(t, c, schema, "upgrade_probe")
	assertPreserved := func() {
		t.Helper()
		if snapshot() != before {
			t.Fatal("migration changed an original business row or credential byte")
		}
		if migrationCatalog(t, c, schema, "upgrade_probe") != catalog {
			t.Fatal("migration changed original columns, constraints or indexes")
		}
		var counter, ratio, revision string
		var createTime, updateTime, observeTime time.Time
		if err := c.QueryRow(ctx, `SELECT u.profile_attributes->>'counter',u.profile_attributes->>'ratio',u.create_time,u.update_time,ci.client_info#>>'{extensions,release,revision}',ci.observe_time FROM user_account u JOIN session s ON s.user_id=u.id JOIN session_client_info ci ON ci.session_id=s.id WHERE u.id=$1 AND s.id=$2`, uid, sid).Scan(&counter, &ratio, &createTime, &updateTime, &revision, &observeTime); err != nil {
			t.Fatal(err)
		}
		if counter != "9007199254740993" || ratio != "0.12345678901234567890123456789" || revision != "9007199254740993" || !createTime.Equal(created) || !updateTime.Equal(observed) || !observeTime.Equal(observed) {
			t.Fatal("exact profile/client numbers or original timestamps changed")
		}
		q := db.New(c)
		clientRow, err := q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: uid, SessionID: sid})
		if err != nil {
			t.Fatal(err)
		}
		client, err := user.UnmarshalClientInfo(clientRow.ClientInfo)
		if err != nil || client == nil || client.ClientType != enum.ClientNative || client.AppName != "Upgrade App" || client.AppVersion != "1.0" || client.AppBuild != "123" {
			t.Fatalf("stored client observation no longer decodes: %v", err)
		}
		identity, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: enum.IdentityEmail, Digests: digester.AllDigests(subject)})
		if err != nil || identity.ID != iid || identity.UserID != uid || identity.CipherKeyVersion == nil || identity.DigestKeyVersion == nil || *identity.DigestKeyVersion != int16(digestVersion) {
			t.Fatalf("original identity is no longer reachable with its HMAC: %v", err)
		}
		if plain, err := cipher.Decrypt(identity.SubjectCiphertext, uint16(*identity.CipherKeyVersion)); err != nil || plain != subject {
			t.Fatalf("original identity cannot be decrypted: %v", err)
		}
		claims, err := signer.Parse(access)
		if err != nil || claims.UserID != uid || claims.SessionID != sid || claims.Scope != "user" || !claims.AuthTime.Equal(authTime) {
			t.Fatalf("pre-migration access credential cannot be verified: %v", err)
		}
		if _, err := q.GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: claims.SessionID, UserID: claims.UserID}); err != nil {
			t.Fatalf("access no longer resolves to its unrevoked stored session: %v", err)
		}
		session, err := q.GetSessionByRefreshHash(ctx, refreshHash[:])
		if err != nil || session.ID != sid || session.UserID != uid || session.RevokeTime != nil || !session.RefreshExpireTime.Equal(deadline) || !session.AuthTime.Equal(authTime) {
			t.Fatalf("pre-migration refresh cannot resolve its live session: %v", err)
		}
		wrongRefresh := sha256.Sum256([]byte("different-synthetic-refresh"))
		if _, err := q.GetSessionByRefreshHash(ctx, wrongRefresh[:]); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("unrelated refresh resolved to a session: %v", err)
		}
		sources, err := q.ListSessionAuthenticationIdentities(ctx, db.ListSessionAuthenticationIdentitiesParams{UserID: uid, SessionID: sid})
		if err != nil || len(sources) != 1 || sources[0].IdentityID != iid || sources[0].Kind != enum.IdentityEmail {
			t.Fatalf("session authentication source no longer resolves: %v", err)
		}
		flow, err := q.GetSignInCompletionFlowByTokenDigest(ctx, hex.EncodeToString(flowHash[:]))
		if err != nil || flow.ID != fid || flow.State != enum.FlowPending || !flow.ExpireTime.Equal(deadline) {
			t.Fatalf("original flow credential cannot resolve its record: %v", err)
		}
		if plain, err := cipher.DecryptWithAAD(flow.Payload, uint16(flow.CipherVersion), flowAAD); err != nil || plain != flowPlain {
			t.Fatalf("flow authenticated payload no longer decrypts: %v", err)
		}
		for _, reject := range []struct{ sql, code string }{
			{`INSERT INTO user_account(id,state) VALUES('invalid',1)`, "23514"},
			{`INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001')`, "23505"},
		} {
			_, err := c.Exec(ctx, reject.sql)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != reject.code {
				t.Fatalf("original constraint no longer rejects invalid write with %s: %v", reject.code, err)
			}
		}
	}
	assertPreserved()
	// Repeat the current nonempty baseline before exercising the next test-only version.
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	assertVersion(t, cfg, schema, latestVersion(t), false)
	assertPreserved()
	for range 2 {
		if err := sourceUp(t, cfg, schema, false); err != nil {
			t.Fatal(err)
		}
		assertVersion(t, cfg, schema, latestVersion(t)+1, false)
		assertPreserved()
	}
	var probeID int
	if err := c.QueryRow(ctx, "SELECT id FROM upgrade_probe").Scan(&probeID); err != nil || probeID != 1 {
		t.Fatalf("test upgrade marker changed: %d %v", probeID, err)
	}
}
func TestMigrationLockReleasedOnFailure(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := sourceUp(t, cfg, schema, true); err == nil {
		t.Fatal("wanted SQL failure")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := lockMigration(ctx, cfg, schema)
	if err != nil {
		t.Fatalf("leaked migration lock: %v", err)
	}
	release()
}
func TestMigrationLockWaitCancellation(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	release, err := lockMigration(ctx, cfg, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waiting, cancel := context.WithTimeout(ctx, 75*time.Millisecond)
	defer cancel()
	if err := Up(waiting, cfg, schema); err == nil {
		t.Fatal("cancelled migration succeeded")
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var count int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", schema).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("cancelled waiter executed DDL")
	}
}

// latestVersion enumerates embedded production sources; test probes always follow them.
func latestVersion(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	latest := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			v, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
			if err != nil {
				t.Fatal(err)
			}
			if v > latest {
				latest = v
			}
		}
	}
	return latest
}
