package user_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
)

func profileAttributesSnapshot(t *testing.T, f *fixture, id string) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(context.Background(), `SELECT jsonb_build_array(profile_attributes,profile_attributes_schema_version,profile_attributes_schema_fingerprint,profile_attributes_update_time)::text FROM user_account WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProfileAttributesLifecycle(t *testing.T) {
	ctx := context.Background()
	f, id := profileAttributesFixture(t)
	v, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-lifecycle-private"}`)}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	before := profileAttributesSnapshot(t, f, id)
	if _, err = f.svc.Freeze(ctx, admin1, id, "synthetic", meta1); err != nil {
		t.Fatal(err)
	}
	frozen, err := f.svc.GetProfileAttributes(ctx, id)
	if err != nil || !reflect.DeepEqual(v, frozen) {
		t.Fatal("freeze changed profile_attributes", err)
	}
	if _, err = f.svc.Unfreeze(ctx, admin1, id, "synthetic", meta1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GetProfileAttributes(ctx, id); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("pending read", err)
	}
	if _, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("pending write", err)
	}
	if profileAttributesSnapshot(t, f, id) != before {
		t.Fatal("cooling period changed profile_attributes")
	}
	if _, err = f.svc.AdminUndeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	if profileAttributesSnapshot(t, f, id) != before {
		t.Fatal("restore changed profile_attributes")
	}
	if _, err = f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatal("purge", n, err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil || string(row.ProfileAttributes) != "{}" || row.ProfileAttributesSchemaVersion != nil || row.ProfileAttributesSchemaFingerprint != nil || row.ProfileAttributesUpdateTime == nil || !row.ProfileAttributesUpdateTime.Equal(*f.clock) {
		t.Fatal("purge must clear profile_attributes and advance metadata", err)
	}
	after := profileAttributesSnapshot(t, f, id)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if profileAttributesSnapshot(t, f, id) != after {
		t.Fatal("repeated purge changed metadata")
	}
}

func TestProfileAttributesPurgeBoundaries(t *testing.T) {
	for _, mode := range []string{"disabled", "mismatch", "neverused", "used_empty"} {
		t.Run(mode, func(t *testing.T) {
			f, id := profileAttributesFixture(t)
			ctx := context.Background()
			if mode != "neverused" {
				_, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-boundary"}`)}, meta1)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "used_empty" {
					if _, err = f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":null}`)}, meta1); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "disabled" {
				newServiceWith(t, f, func(d *user.Deps) { d.ProfileAttributes = nil })
			}
			if mode == "mismatch" {
				newServiceWith(t, f, func(d *user.Deps) {
					d.ProfileAttributes = &user.ProfileAttributesConfig{SchemaVersion: "v2", Schema: []byte(profileAttributesSchema)}
				})
			}
			if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
				t.Fatal(err)
			}
			f.advance(360*time.Hour + time.Second)
			if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			row, err := f.repo.Q().GetUserByID(ctx, id)
			if err != nil || string(row.ProfileAttributes) != "{}" || row.ProfileAttributesSchemaVersion != nil || row.ProfileAttributesSchemaFingerprint != nil {
				t.Fatal("boundary purge metadata", err)
			}
			if mode == "neverused" {
				if row.ProfileAttributesUpdateTime != nil {
					t.Fatal("unused metadata initialized")
				}
			} else if row.ProfileAttributesUpdateTime == nil || !row.ProfileAttributesUpdateTime.Equal(*f.clock) {
				t.Fatal("cleanup time missing")
			}
		})
	}
}

type profileAttributesRollbackAnonymizer struct {
	t    *testing.T
	seen bool
}

func (a *profileAttributesRollbackAnonymizer) Name() string {
	return "synthetic_profile_attributes_host"
}
func (a *profileAttributesRollbackAnonymizer) Tables() []string { return nil }
func (a *profileAttributesRollbackAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, id string) error {
	var empty bool
	if err := tx.QueryRow(ctx, `SELECT profile_attributes='{}'::jsonb AND profile_attributes_schema_version IS NULL AND profile_attributes_schema_fingerprint IS NULL AND state=$2 FROM user_account WHERE id=$1`, id, enum.UserDeleted).Scan(&empty); err != nil {
		a.t.Fatal(err)
	}
	if !empty {
		a.t.Error("host callback must see cleared profile_attributes in same transaction")
	}
	a.seen = true
	return errors.New("synthetic host down")
}
func TestProfileAttributesPurgeRollback(t *testing.T) {
	f, id := profileAttributesFixture(t)
	ctx := context.Background()
	if _, err := f.svc.PatchProfileAttributes(ctx, id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-rollback-private"}`)}, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := &profileAttributesRollbackAnonymizer{t: t}
	newServiceWith(t, f, func(d *user.Deps) { d.Anonymizers = []anonymize.Anonymizer{a} })
	f.advance(360*time.Hour + time.Second)
	if n, err := f.svc.PurgeDueUsers(ctx); err == nil || n != 0 || !a.seen {
		t.Fatal("expected callback rollback", n, err)
	}
	after, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("callback failure must restore entire account row", err)
	}
}
