package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

const recoveryExtensionsData = `{"count":9007199254740993,"label":"recovery-final"}`

func recoveryExtensionsConfig() *user.ExtensionsConfig {
	return &user.ExtensionsConfig{SchemaVersion: "recovery-v1", Schema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"count":{"type":"integer"},"label":{"type":"string"}},"additionalProperties":false}`)}
}

func seedRecoveryUserExtensions(t *testing.T, a *accountkit.Auth, id string) user.ExtensionsView {
	t.Helper()
	ctx := context.Background()
	z := int64(0)
	v, err := a.Users().PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"count":9007199254740993,"label":"recovery-first"}`), ExpectedRevision: &z}, user.Meta{})
	if err != nil || v.Revision != 1 {
		t.Fatalf("first recovery extension revision: %d %v", v.Revision, err)
	}
	v, err = a.Users().PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"label":"recovery-final"}`), ExpectedRevision: &v.Revision}, user.Meta{})
	if err != nil || v.Revision != 2 || string(v.Data) != recoveryExtensionsData || v.SchemaVersion == nil || *v.SchemaVersion != "recovery-v1" || v.SchemaFingerprint == nil || len(*v.SchemaFingerprint) != 64 || v.UpdateTime == nil || v.UpdateTime.IsZero() {
		t.Fatalf("recovery extensions were not initialized with exact data and identity: %+v %v", v, err)
	}
	return v
}

func assertRecoveryUserExtensions(t *testing.T, a *accountkit.Auth, state recoveryState) {
	t.Helper()
	got, err := a.Users().GetExtensions(context.Background(), state.Fixture.UserID)
	want := state.Extensions
	if err != nil || !bytes.Equal(got.Data, want.Data) || string(got.Data) != recoveryExtensionsData || got.Revision != 2 || got.Revision != want.Revision || got.SchemaVersion == nil || want.SchemaVersion == nil || *got.SchemaVersion != "recovery-v1" || *got.SchemaVersion != *want.SchemaVersion || got.SchemaFingerprint == nil || want.SchemaFingerprint == nil || *got.SchemaFingerprint != *want.SchemaFingerprint || got.UpdateTime == nil || want.UpdateTime == nil || !got.UpdateTime.Equal(*want.UpdateTime) {
		t.Fatalf("recovered extensions data, schema identity, revision or time differs: %+v %v", got, err)
	}
}

// This test exercises the existing whole-row snapshot. Omitting any extension
// column would hide corruption from both restore and source-unchanged checks.
func TestRecoveryUserExtensionsSnapshot(t *testing.T) {
	cfg := minimal()
	cfg.UserExtensions = recoveryExtensionsConfig()
	a, pool, _ := integrationInstance(t, cfg, testRedis(t))
	f, _ := seedFormatFixture(t, pool, a.Config())
	state := recoveryState{Fixture: f, Extensions: seedRecoveryUserExtensions(t, a, f.UserID)}
	assertRecoveryUserExtensions(t, a, state)
	before := snapshotRecovery(t, pool)
	for _, tc := range []struct{ name, sql string }{
		{"data", `UPDATE user_account SET extensions='{"count":9007199254740992,"label":"recovery-final"}' WHERE id=$1`},
		{"schema version", `UPDATE user_account SET extensions_schema_version='recovery-v2' WHERE id=$1`},
		{"schema fingerprint", `UPDATE user_account SET extensions_schema_fingerprint=repeat('0',64) WHERE id=$1`},
		{"revision", `UPDATE user_account SET extensions_revision=extensions_revision+1 WHERE id=$1`},
		{"update time", `UPDATE user_account SET extensions_update_time=extensions_update_time+interval '1 second' WHERE id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(context.Background(), tc.sql, f.UserID); err != nil {
				t.Fatal(err)
			}
			after := snapshotRecovery(t, pool)
			if after == before {
				t.Fatal("recovery/source-unchanged snapshot omitted changed extension column")
			}
			before = after
		})
	}
	// Even after the synthetic historical Schema mismatch above, cleanup must
	// erase the stored data without attempting validation against current Schema.
	assertRecoveryUserExtensionsCleanup(t, a, pool, f.UserID, 4)
}

func assertRecoveryUserExtensionsCleanup(t *testing.T, a *accountkit.Auth, pool *pgxpool.Pool, id string, revision int64) {
	t.Helper()
	ctx := context.Background()
	admin := user.Admin{Issuer: "https://recovery.example.test", Subject: "recovery-cleanup", Username: "recovery-test"}
	if _, err := a.Users().AdminDeleteUser(ctx, admin, id, user.Meta{}); err != nil {
		t.Fatal("recovery cleanup delete:", err)
	}
	// This precise synthetic fixture row is in a disposable test database. Move
	// its deletion deadline into the past instead of sleeping through cooling.
	if _, err := pool.Exec(ctx, `UPDATE user_account SET delete_time=now()-interval '16 days', purge_time=now()-interval '1 day' WHERE id=$1 AND state=$2`, id, enum.UserPendingDeletion); err != nil {
		t.Fatal(err)
	}
	if n, err := a.Users().PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatalf("recovery cleanup purge count=%d err=%v", n, err)
	}
	var clean bool
	if err := pool.QueryRow(ctx, `SELECT state=$2 AND extensions='{}'::jsonb AND extensions_schema_version IS NULL AND extensions_schema_fingerprint IS NULL AND extensions_revision=$3 AND extensions_update_time IS NOT NULL FROM user_account WHERE id=$1`, id, enum.UserDeleted, revision).Scan(&clean); err != nil || !clean {
		t.Fatalf("recovery cleanup retained extensions or lost cleanup metadata: clean=%v err=%v", clean, err)
	}
}

// Removing extension state must fail before a backup can silently lose its
// independent expected values, in particular exact numbers and nullable metadata.
func TestRecoveryUserExtensionsState(t *testing.T) {
	const expected = `{"Data":{"count":9007199254740993,"label":"recovery-final"},"SchemaVersion":"recovery-v1","SchemaFingerprint":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","Revision":2,"UpdateTime":"2026-10-21T01:02:03.456789Z"}`
	var state recoveryState
	if err := json.Unmarshal([]byte(`{"Extensions":`+expected+`}`), &state); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["Extensions"]) != expected {
		t.Fatalf("recovery state dropped or changed exact extension data/metadata: %s", fields["Extensions"])
	}
}
