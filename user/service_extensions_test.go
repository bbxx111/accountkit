package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user"
)

const extensionsSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"theme":{"type":"string"},"score":{"type":"number"}},"additionalProperties":false}`

func extensionsFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	newServiceWith(t, f, func(d *user.Deps) {
		d.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "v1", Schema: json.RawMessage(extensionsSchema)}
	})
	return f, f.signIn(t, enum.IdentityPhone, phone1, dev1).UserID
}

func TestUserExtensionsJSONBRepresentationBudget(t *testing.T) {
	f := newFixture(t)
	schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"entries":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`
	newServiceWith(t, f, func(d *user.Deps) {
		d.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "v1", Schema: []byte(schema)}
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
	z := int64(0)
	_, err = f.svc.PatchExtensions(context.Background(), id, user.ExtensionsPatch{Document: raw, ExpectedRevision: &z}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = f.pool.QueryRow(context.Background(), `SELECT extensions::text FROM user_account WHERE id=$1`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) <= 16384 {
		t.Fatalf("fixture must expose jsonb whitespace inflation: raw=%d stored=%d", len(raw), len(stored))
	}
	v, err := f.svc.GetExtensions(context.Background(), id)
	if err != nil || len(v.Data) != len(raw) {
		t.Fatalf("valid normalized-size document cannot be read: %v (raw=%d stored=%d)", err, len(raw), len(stored))
	}
	_, err = f.svc.PatchExtensions(context.Background(), id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: &v.Revision}, meta1)
	if err != nil {
		t.Fatal("stored representation cannot be patched:", err)
	}
}
func TestUserExtensions(t *testing.T) {
	f, id := extensionsFixture(t)
	ctx := context.Background()
	zero := int64(0)
	v, err := f.svc.GetExtensions(ctx, id)
	if err != nil || string(v.Data) != "{}" || v.Revision != 0 || v.SchemaVersion != nil || v.UpdateTime != nil {
		t.Fatalf("initial: %+v %v", v, err)
	}
	if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{}`)}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("missing revision: %v", err)
	}
	v, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-private","score":1}`), ExpectedRevision: &zero}, meta1)
	if err != nil || v.Revision != 1 || v.SchemaVersion == nil || *v.SchemaVersion != "v1" {
		t.Fatalf("write: %+v %v", v, err)
	}
	one := int64(1)
	before := *v.UpdateTime
	v, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"score":1e0}`), ExpectedRevision: &one}, meta1)
	if err != nil || v.Revision != 1 || !v.UpdateTime.Equal(before) {
		t.Fatalf("noop: %+v %v", v, err)
	}
	if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"unknown-private":null}`), ExpectedRevision: &zero}, meta1); !errors.Is(err, user.ErrExtensionsConflict) {
		t.Fatalf("revision priority: %v", err)
	}
	_, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"unknown-private":null}`), ExpectedRevision: &one}, meta1)
	var ve *user.ExtensionsValidationError
	if !errors.Is(err, user.ErrInvalidArgument) || !errors.As(err, &ve) || len(ve.Violations) != 1 || ve.Violations[0].Path != "" {
		t.Fatalf("safe validation: %v", err)
	}
	v.Data[0] = 'x'
	read, err := f.svc.GetExtensions(ctx, id)
	if err != nil || read.Data[0] != '{' {
		t.Fatal("view aliases storage")
	}
	mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, id, enum.UserFrozen)
	if _, err = f.svc.GetExtensions(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: &one}, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("frozen: %v", err)
	}
	mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, id, enum.UserPendingDeletion)
	if _, err = f.svc.GetExtensions(ctx, id); !errors.Is(err, user.ErrInvalidState) {
		t.Fatalf("pending: %v", err)
	}
	unknown, _ := ids.New(ids.User)
	if _, err = f.svc.GetExtensions(ctx, unknown); !errors.Is(err, user.ErrNotFound) {
		t.Fatal(err)
	}
}
func TestUserExtensionsSchemaAndLimit(t *testing.T) {
	f, id := extensionsFixture(t)
	ctx := context.Background()
	z := int64(0)
	cfg := f.deps.UserExtensions
	cfg.Schema[0] = 'x'
	if _, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"a"}`), ExpectedRevision: &z}, meta1); err != nil {
		t.Fatal(err)
	}
	newServiceWith(t, f, func(d *user.Deps) {
		d.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "v2", Schema: []byte(extensionsSchema)}
	})
	one := int64(1)
	if _, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: &one}, meta1); !errors.Is(err, user.ErrExtensionsSchemaMismatch) {
		t.Fatal(err)
	}
	mustExec(t, f, `UPDATE user_account SET extensions_revision=$2 WHERE id=$1`, id, int64(math.MaxInt64))
	newServiceWith(t, f, func(d *user.Deps) {
		d.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "v1", Schema: []byte(extensionsSchema)}
	})
	max := int64(math.MaxInt64)
	if _, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"b"}`), ExpectedRevision: &max}, meta1); !errors.Is(err, user.ErrExtensionsConflict) {
		t.Fatal(err)
	}
}
func TestExtensionsConfiguration(t *testing.T) {
	if err := user.ValidateExtensionsConfig(nil); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range []*user.ExtensionsConfig{{SchemaVersion: "bad version", Schema: []byte(extensionsSchema)}, {SchemaVersion: "v1", Schema: []byte(`{}`)}} {
		if err := user.ValidateExtensionsConfig(cfg); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	f := newFixture(t)
	if _, err := f.svc.GetExtensions(context.Background(), "invalid"); !errors.Is(err, user.ErrExtensionsNotConfigured) {
		t.Fatal(err)
	}
}

func TestUserExtensionsMetadataAndLegacyViews(t *testing.T) {
	f, id := extensionsFixture(t)
	ctx := context.Background()
	z := int64(0)
	initial, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: &z}, meta1); err != nil {
		t.Fatal(err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil || !row.UpdateTime.Equal(initial.UpdateTime) || row.ExtensionsRevision != 0 || row.ExtensionsSchemaVersion != nil {
		t.Fatal("initial no-op changed metadata", err)
	}
	for _, rev := range []*int64{nil, func() *int64 { x := int64(-1); return &x }()} {
		if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: rev}, meta1); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	v, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-hidden"}`), ExpectedRevision: &z}, meta1)
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
		if strings.Contains(strings.ToLower(string(raw)), "extensions") || strings.Contains(string(raw), "synthetic-hidden") {
			t.Fatal("legacy view leaked extensions")
		}
	}
	v, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":null}`), ExpectedRevision: &v.Revision}, meta1)
	if err != nil || string(v.Data) != "{}" || v.Revision != 2 || v.SchemaVersion == nil {
		t.Fatalf("cleared document lost binding: %+v %v", v, err)
	}
	mustExec(t, f, `UPDATE user_account SET extensions_revision=0 WHERE id=$1`, id)
	if _, err = f.svc.GetExtensions(ctx, id); err == nil || errors.Is(err, user.ErrInvalidArgument) {
		t.Fatalf("corrupt metadata must fail internally: %v", err)
	}
}
