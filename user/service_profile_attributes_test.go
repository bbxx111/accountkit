package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

const profileAttributesSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"theme":{"type":"string"},"score":{"type":"number"}},"additionalProperties":false}`

func TestProfileAttributesSchemaEvolution(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"old"}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	schema := strings.Replace(profileAttributesSchema, `"theme":{"type":"string"}`, `"theme":{"type":"string","description":"compatible annotation"}`, 1)
	newServiceWith(t, f, func(d *user.Deps) { d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(schema)} })
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"score":2}`)}, meta1); err != nil {
		t.Fatalf("compatible schema evolution rejected: %v", err)
	}
	for _, next := range []string{
		strings.Replace(profileAttributesSchema, `"score":{"type":"number"}`, `"score":{"type":"number"},"optional":{"type":"boolean"}`, 1),
		strings.Replace(profileAttributesSchema, `"theme":{"type":"string"}`, `"theme":{"type":"string","maxLength":100}`, 1),
	} {
		newServiceWith(t, f, func(d *user.Deps) { d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(next)} })
		if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"score":3}`)}, meta1); err != nil {
			t.Fatal("compatible schema rejected", err)
		}
	}
	tight := strings.Replace(profileAttributesSchema, `"theme":{"type":"string"}`, `"theme":{"type":"string","maxLength":2}`, 1)
	newServiceWith(t, f, func(d *user.Deps) { d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(tight)} })
	before, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if v, err := f.svc.GetProfileAttributes(ctx, id); err != nil || !strings.Contains(string(v.Data), "old") {
		t.Fatal("historical read rejected", err)
	}
	f.advance(time.Second)
	for _, patch := range []string{`{}`, `{"score":4}`} {
		if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(patch)}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatal("invalid complete candidate accepted", err)
		}
	}
	after, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if string(before.ProfileAttributes) != string(after.ProfileAttributes) || !before.UpdateTime.Equal(after.UpdateTime) {
		t.Fatal("rejected candidate changed account")
	}
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"ok","score":4}`)}, meta1); err != nil {
		t.Fatal("same patch repair rejected", err)
	}
	after, _ = user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if !after.UpdateTime.Equal(*f.clock) {
		t.Fatal("changed document did not update account time")
	}
	loose := strings.Replace(tight, `"maxLength":2`, `"maxLength":100`, 1)
	newServiceWith(t, f, func(d *user.Deps) { d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(loose)} })
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"relaxed"}`)}, meta1); err != nil {
		t.Fatal("relaxed schema rejected", err)
	}
	removed := strings.Replace(profileAttributesSchema, `"theme":{"type":"string"},`, "", 1)
	newServiceWith(t, f, func(d *user.Deps) { d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(removed)} })
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":null}`)}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatal("unknown deletion accepted", err)
	}
}

func profileAttributesFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: json.RawMessage(profileAttributesSchema)}
	})
	return f, f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
}

func TestProfileAttributesJSONBRepresentationBudget(t *testing.T) {
	f := newFixture(t)
	schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"entries":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`
	newServiceWith(t, f, func(d *user.Deps) {
		d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(schema)}
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
	if err != nil || string(v.Data) != "{}" {
		t.Fatalf("initial: %+v %v", v, err)
	}

	v, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-private","score":1}`)}, meta1)
	if err != nil {
		t.Fatalf("write: %+v %v", v, err)
	}
	before, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	f.advance(time.Second)
	v, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"score":1e0}`)}, meta1)
	after, _ := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if err != nil || !after.UpdateTime.Equal(before.UpdateTime) {
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
		d.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(profileAttributesSchema)}
	})
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1); err != nil {
		t.Fatal(err)
	}

}
func TestProfileAttributesConfiguration(t *testing.T) {
	if err := user.ValidateProfileAttributesConfig(nil); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []*user.ProfileAttributesConfig{{Schema: []byte(`{}`)}} {
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
	initial, err := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	row, err := user.TestRepoQueries(f.repo).GetUserByID(ctx, id)
	if err != nil || !row.UpdateTime.Equal(initial.UpdateTime) {
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
	if err != nil || string(v.Data) != "{}" {
		t.Fatalf("cleared document lost binding: %+v %v", v, err)
	}
	mustExec(t, f, `UPDATE user_account SET profile_attributes='[]'::jsonb WHERE id=$1`, id)
	if _, err = f.svc.GetProfileAttributes(ctx, id); err == nil || errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("corrupt metadata must fail internally: %v", err)
	}
}
