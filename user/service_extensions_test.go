package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

const profileAttributesSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"theme":{"type":"string"},"score":{"type":"number"}},"additionalProperties":false}`

func profileAttributesFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "v1", Schema: json.RawMessage(profileAttributesSchema)}
	})
	return f, f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
}

func TestProfileAttributesJSONBRepresentationBudget(t *testing.T) {
	f := newFixture(t)
	schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"entries":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`
	newServiceWith(t, f, func(d *user.Deps) {
		d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "v1", Schema: []byte(schema)}
	})
	id := f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
	entries := make([]string, 100)
	for i := range entries {
		entries[i] = strings.Repeat("x", 160)
	}
	raw, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 16384 {
		t.Fatal("fixture too large")
	}
	var representation string
	if err = f.pool.QueryRow(context.Background(), `SELECT $1::jsonb::text`, string(raw)).Scan(&representation); err != nil {
		t.Fatal(err)
	}
	t.Logf("compact=%d jsonb_text=%d", len(raw), len(representation))
	_, err = f.svc.PatchProfileAttributes(context.Background(), id, user.ProfileAttributesPatch{Document: raw}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = f.pool.QueryRow(context.Background(), `SELECT profile_attributes::text FROM user_account WHERE id=$1`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) <= 16384 {
		t.Fatalf("fixture must expose jsonb whitespace inflation: raw=%d stored=%d", len(raw), len(stored))
	}
	v, err := f.svc.GetProfileAttributes(context.Background(), id)
	if err != nil || len(v.Data) != len(raw) {
		t.Fatalf("valid normalized-size document cannot be read: %v (raw=%d stored=%d)", err, len(raw), len(stored))
	}
	_, err = f.svc.PatchProfileAttributes(context.Background(), id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1)
	if err != nil {
		t.Fatal("stored representation cannot be patched:", err)
	}
}
func TestProfileAttributes(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	v, err := f.svc.GetProfileAttributes(ctx, id)
	if err != nil || string(v.Data) != "{}" || v.SchemaVersion != nil || v.UpdateTime != nil {
		t.Fatalf("initial: %+v %v", v, err)
	}

	v, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-private","score":1}`)}, meta1)
	if err != nil || v.SchemaVersion == nil || *v.SchemaVersion != "v1" {
		t.Fatalf("write: %+v %v", v, err)
	}
	before := *v.UpdateTime
	v, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"score":1e0}`)}, meta1)
	if err != nil || !v.UpdateTime.Equal(before) {
		t.Fatalf("noop: %+v %v", v, err)
	}

	_, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"unknown-private":null}`)}, meta1)
	var ve *user.ProfileAttributesValidationError
	if !errors.Is(err, user.ErrInvalidArgument) || !errors.As(err, &ve) || len(ve.Violations) != 1 || ve.Violations[0].Path != "" {
		t.Fatalf("safe validation: %v", err)
	}
	v.Data[0] = 'x'
	read, err := f.svc.GetProfileAttributes(ctx, id)
	if err != nil || read.Data[0] != '{' {
		t.Fatal("view aliases storage")
	}
	mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, id, enum.UserFrozen)
	if _, err = f.svc.GetProfileAttributes(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("frozen: %v", err)
	}
	mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, id, enum.UserPendingDeletion)
	if _, err = f.svc.GetProfileAttributes(ctx, id); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("pending: %v", err)
	}
	unknown, _ := ids.New(ids.User)
	if _, err = f.svc.GetProfileAttributes(ctx, unknown); !errors.Is(err, user.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestProfileAttributesSchemaAndLimit(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	cfg := f.deps.ProfileAttributes
	cfg.Schema[0] = 'x'
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"a"}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	newServiceWith(t, f, func(d *user.Deps) {
		d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "v2", Schema: []byte(profileAttributesSchema)}
	})
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1); !errors.Is(err, user.ErrProfileAttributesSchemaMismatch) {
		t.Fatal(err)
	}

}
func TestProfileAttributesConfiguration(t *testing.T) {
	if err := user.ValidateProfileAttributesConfig(nil); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []*user.ProfileAttributesConfig{{SchemaVersion: "bad version", Schema: []byte(profileAttributesSchema)}, {SchemaVersion: "v1", Schema: []byte(`{}`)}} {
		if err := user.ValidateProfileAttributesConfig(cfg); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	f := newFixture(t)
	if _, err := f.svc.GetProfileAttributes(context.Background(), "invalid"); !errors.Is(err, user.ErrProfileAttributesNotConfigured) {
		t.Fatal(err)
	}
}

func TestProfileAttributesMetadataAndLegacyViews(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	initial, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil || !row.UpdateTime.Equal(initial.UpdateTime) || row.ProfileAttributesSchemaVersion != nil {
		t.Fatal("initial no-op changed metadata", err)
	}
	v, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-hidden"}`)}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	me, err := f.svc.GetMe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := f.svc.GetUserDetail(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := f.svc.BatchPublicProfiles(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{me, admin, profiles} {
		raw, _ := json.Marshal(value)
		if strings.Contains(strings.ToLower(string(raw)), "profile_attributes") || strings.Contains(string(raw), "synthetic-hidden") {
			t.Fatal("legacy view leaked profile_attributes")
		}
	}
	v, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":null}`)}, meta1)
	if err != nil || string(v.Data) != "{}" || v.SchemaVersion == nil {
		t.Fatalf("cleared document lost binding: %+v %v", v, err)
	}
	mustExec(t, f, `UPDATE user_account SET profile_attributes_update_time=NULL WHERE id=$1`, id)
	if _, err = f.svc.GetProfileAttributes(ctx, id); err == nil || errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("corrupt metadata must fail internally: %v", err)
	}
}
