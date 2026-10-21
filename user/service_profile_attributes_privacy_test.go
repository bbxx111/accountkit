package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgconn"
)

func profileAttributesNoSecrets(t *testing.T, value any, secrets ...string) {
	t.Helper()
	text := fmt.Sprintf("%+v", value)
	b, _ := json.Marshal(value)
	text += string(b)
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Fatal("synthetic private marker exposed")
		}
	}
}

func profileAttributesSafeErrorChain(t *testing.T, err error, secrets ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected failed extension operation")
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatal("extension error retains raw database detail")
	}
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		profileAttributesNoSecrets(t, e, secrets...)
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, c := range multi.Unwrap() {
				walk(c)
			}
		} else {
			walk(errors.Unwrap(e))
		}
	}
	walk(err)
	var ve *user.ProfileAttributesValidationError
	if errors.As(err, &ve) {
		profileAttributesNoSecrets(t, ve.Violations, secrets...)
	}
}

func TestProfileAttributesValidatorErrorPrivacy(t *testing.T) {
	for _, tc := range []struct{ name, schema, patch string }{
		{"enum", `{"type":"string","enum":["synthetic-enum-candidate"]}`, `{"theme":"synthetic-secret-input"}`},
		{"const", `{"type":"string","const":"synthetic-const-candidate"}`, `{"theme":"synthetic-secret-input"}`},
		{"unknown", `{"type":"string"}`, `{"synthetic_unknown_private":null}`},
		{"nested_unknown", `{"type":"object","properties":{"allowed":{"type":"string"}},"additionalProperties":false}`, `{"theme":{"synthetic_unknown_private":null}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, id := profileAttributesFixture(t)
			var logs bytes.Buffer
			schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"theme":` + tc.schema + `},"additionalProperties":false}`
			newServiceWith(t, f, func(d *user.Deps) {
				d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
				d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "v1", Schema: []byte(schema)}
			})
			_, err := f.svc.PatchProfileAttributes(context.Background(), id, user.ProfileAttributesPatch{Document: []byte(tc.patch)}, meta1)
			if !errors.Is(err, user.ErrInvalidArgument) {
				t.Fatal("wrong validation class", err)
			}
			secrets := []string{"synthetic-enum-candidate", "synthetic-const-candidate", "synthetic-secret-input", "synthetic_unknown_private"}
			profileAttributesSafeErrorChain(t, err, secrets...)
			profileAttributesNoSecrets(t, logs.String(), secrets...)
			for _, e := range f.audit.RawEvents() {
				if e.Action == "USER_PROFILE_ATTRIBUTES_UPDATED" {
					t.Fatal("validation failure emitted success")
				}
			}
		})
	}
}

func TestProfileAttributesStorageErrorPrivacy(t *testing.T) {
	for _, phase := range []string{"update", "commit"} {
		t.Run(phase, func(t *testing.T) {
			f, id := profileAttributesFixture(t)
			ctx := context.Background()
			var logs bytes.Buffer
			newServiceWith(t, f, func(d *user.Deps) { d.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
			before := profileAttributesSnapshot(t, f, id)
			mustExec(t, f, `CREATE FUNCTION synthetic_profile_attributes_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic-sql-private-detail'; END $$`)
			if phase == "update" {
				mustExec(t, f, `CREATE TRIGGER synthetic_profile_attributes_fail BEFORE UPDATE ON user_account FOR EACH ROW EXECUTE FUNCTION synthetic_profile_attributes_fail()`)
			} else {
				mustExec(t, f, `CREATE CONSTRAINT TRIGGER synthetic_profile_attributes_fail AFTER UPDATE ON user_account DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION synthetic_profile_attributes_fail()`)
			}
			v, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-sql-private-value"}`)}, meta1)
			profileAttributesSafeErrorChain(t, err, "synthetic-sql-private-detail", "synthetic-sql-private-value")
			if v.Data != nil || profileAttributesSnapshot(t, f, id) != before {
				t.Fatal("failed storage transaction changed profile_attributes or returned success")
			}
			profileAttributesNoSecrets(t, logs.String(), "synthetic-sql-private-detail", "synthetic-sql-private-value")
			for _, e := range f.audit.RawEvents() {
				if e.Action == "USER_PROFILE_ATTRIBUTES_UPDATED" {
					t.Fatal("storage failure emitted success")
				}
			}
		})
	}
}

func TestProfileAttributesPurgeFailureLogPrivacy(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	var logs bytes.Buffer
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-purge-private-value"}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	a := &profileAttributesRollbackAnonymizer{t: t}
	newServiceWith(t, f, func(d *user.Deps) {
		d.Anonymizers = []anonymize.Anonymizer{a}
		d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	})
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	_, err := f.svc.PurgeDueUsers(ctx)
	if err == nil || !strings.Contains(err.Error(), "synthetic host down") {
		t.Fatal("legacy callback error contract changed", err)
	}
	profileAttributesNoSecrets(t, err, "synthetic-purge-private-value")
	profileAttributesNoSecrets(t, logs.String(), "synthetic-purge-private-value")
	if !strings.Contains(logs.String(), "purge failed") {
		t.Fatal("failure logger not exercised")
	}
}

func TestProfileAttributesAuditLateInsertPrivacy(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	r := &profileAttributesRecorder{f: f}
	opts := audit.StoreOptions{Source: "synthetic/profile_attributes", Namespace: "synthetic/profile_attributes"}
	newServiceWith(t, f, func(d *user.Deps) { d.Audit = r; d.AuditSource = opts.Source; d.AuditNamespace = opts.Namespace })
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-late-private"}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	if len(r.events) != 1 {
		t.Fatal("missing captured event")
	}
	e := r.events[0]
	profileAttributesNoSecrets(t, e, "theme", "synthetic-late-private", "properties")
	if e.Actor.Type != audit.ActorService || e.Actor.Namespace != opts.Namespace || len(e.Targets) != 1 || e.Targets[0].Type != "USER" || e.Targets[0].Namespace != opts.Namespace || e.Targets[0].ID != id {
		t.Fatal("extension event lacks scoped local USER reference")
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	store, err := audit.NewStoreWithOptions(f.pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.InsertBatch(ctx, []audit.Event{e}); err != nil || result.Written != 1 {
		t.Fatal("late first insert", result, err)
	}
	page, err := store.ListByResource(ctx, audit.Query{Source: opts.Source, Resource: e.Targets[0], Limit: 10})
	if err != nil || len(page.Records) != 1 {
		t.Fatal("late record missing", err)
	}
	profileAttributesNoSecrets(t, page.Records, "theme", "synthetic-late-private", "properties")
	if !bytes.Equal(page.Records[0].Event.Data, e.Data) {
		t.Fatal("safe schema audit facts changed")
	}
	if _, err := f.svc.GetProfileAttributes(ctx, id); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("deleted profile_attributes became readable", err)
	}
}
