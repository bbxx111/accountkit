package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/bbxx111/accountkit/internal/userdb"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

const recoveryAvatarURL = "https://example.test/recovery-avatar.png?version=1"

const recoveryProfileAttributesData = `{"count":9007199254740993,"label":"recovery-final"}`

func TestRecoveryAvatarFixture(t *testing.T) {
	cfg := minimal()
	cfg.ProfileAttributes = recoveryProfileAttributesConfig()
	a, pool, _ := integrationInstance(t, cfg, testRedis(t))
	f, _ := seedSyntheticAccount(t, pool, a.Config())
	seedRecoveryProfileAttributes(t, a, f.UserID)
	m, err := a.Users().GetMe(context.Background(), f.UserID)
	if err != nil || m.AvatarURL != "https://example.test/recovery-avatar.png?version=1" {
		t.Fatalf("recovery seed omitted avatar: %+v %v", m, err)
	}
	before := snapshotRecovery(t, pool)
	if _, err := pool.Exec(context.Background(), `UPDATE user_account SET avatar_url='https://example.test/changed.png' WHERE id=$1`, f.UserID); err != nil {
		t.Fatal(err)
	}
	if before == snapshotRecovery(t, pool) {
		t.Fatal("source-unchanged snapshot omitted avatar")
	}
	assertRecoveryProfileAttributesCleanup(t, a, pool, f.UserID)
	row, err := db.New(pool).GetUserByID(context.Background(), f.UserID)
	if err != nil || row.AvatarUrl != nil {
		t.Fatal("recovered avatar purge", err)
	}
}

func recoveryProfileAttributesConfig() *user.ProfileAttributesConfig {
	return &user.ProfileAttributesConfig{Schema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"count":{"type":"integer"},"label":{"type":"string"}},"additionalProperties":false}`)}
}

func seedRecoveryProfileAttributes(t *testing.T, a *accountkit.Auth, id string) user.ProfileAttributesView {
	t.Helper()
	ctx := context.Background()
	avatar := recoveryAvatarURL
	if _, err := a.Users().UpdateProfile(ctx, id, user.ProfilePatch{AvatarURL: &avatar}); err != nil {
		t.Fatalf("recovery avatar seed: %v", err)
	}
	v, err := a.Users().PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"count":9007199254740993,"label":"recovery-first"}`)}, user.Meta{})
	if err != nil {
		t.Fatalf("first recovery extension write: %v", err)
	}
	v, err = a.Users().PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"label":"recovery-final"}`)}, user.Meta{})
	if err != nil || string(v.Data) != recoveryProfileAttributesData {
		t.Fatalf("recovery profile_attributes were not initialized with exact data: %+v %v", v, err)
	}
	return v
}

func assertRecoveryProfileAttributes(t *testing.T, a *accountkit.Auth, state recoveryState) {
	t.Helper()
	got, err := a.Users().GetProfileAttributes(context.Background(), state.Fixture.UserID)
	me, avatarErr := a.Users().GetMe(context.Background(), state.Fixture.UserID)
	if avatarErr != nil || state.AvatarURL != recoveryAvatarURL || me.AvatarURL != state.AvatarURL {
		t.Fatalf("recovery avatar differs: %+v %v", me, avatarErr)
	}
	want := state.ProfileAttributes
	if err != nil || !bytes.Equal(got.Data, want.Data) || string(got.Data) != recoveryProfileAttributesData {
		t.Fatalf("recovered profile_attributes data differs: %+v %v", got, err)
	}
}

// This test exercises the existing whole-row snapshot. Omitting any extension
// data or account update_time would hide corruption from both restore and
// source-unchanged checks.
func TestRecoveryProfileAttributesSnapshot(t *testing.T) {
	cfg := minimal()
	cfg.ProfileAttributes = recoveryProfileAttributesConfig()
	a, pool, _ := integrationInstance(t, cfg, testRedis(t))
	f, _ := seedSyntheticAccount(t, pool, a.Config())
	state := recoveryState{Fixture: f, ProfileAttributes: seedRecoveryProfileAttributes(t, a, f.UserID), AvatarURL: recoveryAvatarURL}
	assertRecoveryProfileAttributes(t, a, state)
	before := snapshotRecovery(t, pool)
	for _, tc := range []struct{ name, sql string }{
		{"data", `UPDATE user_account SET profile_attributes='{"count":9007199254740992,"label":"recovery-final"}' WHERE id=$1`},
		{"account update time", `UPDATE user_account SET update_time=update_time+interval '1 second' WHERE id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(context.Background(), tc.sql, f.UserID); err != nil {
				t.Fatal(err)
			}
			after := snapshotRecovery(t, pool)
			if after == before {
				t.Fatal("recovery/source-unchanged snapshot omitted changed profile data or account update_time")
			}
			before = after
		})
	}
	// Cleanup must erase the restored JSON in the anonymization transaction.
	assertRecoveryProfileAttributesCleanup(t, a, pool, f.UserID)
}

func assertRecoveryProfileAttributesCleanup(t *testing.T, a *accountkit.Auth, pool *pgxpool.Pool, id string) {
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
	if err := pool.QueryRow(ctx, `SELECT state=$2 AND profile_attributes='{}'::jsonb AND avatar_url IS NULL FROM user_account WHERE id=$1`, id, enum.UserDeleted).Scan(&clean); err != nil || !clean {
		t.Fatalf("recovery cleanup retained profile_attributes: clean=%v err=%v", clean, err)
	}
}

// Removing extension state must fail before a backup can silently lose its
// independent expected values, in particular exact numbers and complete JSON.
func TestRecoveryProfileAttributesState(t *testing.T) {
	const expected = `{"Data":{"count":9007199254740993,"label":"recovery-final"}}`
	var state recoveryState
	if err := json.Unmarshal([]byte(`{"ProfileAttributes":`+expected+`}`), &state); err != nil {
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
	if string(fields["ProfileAttributes"]) != expected {
		t.Fatalf("recovery state dropped or changed exact profile data: %s", fields["ProfileAttributes"])
	}
}
