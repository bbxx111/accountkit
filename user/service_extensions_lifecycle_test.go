package user_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5"
)

func extensionsSnapshot(t *testing.T, f *fixture, id string) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(context.Background(), `SELECT jsonb_build_array(extensions,extensions_schema_version,extensions_schema_fingerprint,extensions_revision,extensions_update_time)::text FROM user_account WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUserExtensionsLifecycle(t *testing.T) {
	ctx := context.Background()
	f, id := extensionsFixture(t)
	z := int64(0)
	v, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-lifecycle-private"}`), ExpectedRevision: &z}, meta1)
	if err != nil {
		t.Fatal(err)
	}
	before := extensionsSnapshot(t, f, id)
	if _, err = f.svc.Freeze(ctx, admin1, id, "synthetic", meta1); err != nil {
		t.Fatal(err)
	}
	frozen, err := f.svc.GetExtensions(ctx, id)
	if err != nil || !reflect.DeepEqual(v, frozen) {
		t.Fatal("freeze changed extensions", err)
	}
	if _, err = f.svc.Unfreeze(ctx, admin1, id, "synthetic", meta1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GetExtensions(ctx, id); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("pending read", err)
	}
	if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: &v.Revision}, meta1); !errors.Is(err, user.ErrInvalidState) {
		t.Fatal("pending write", err)
	}
	if extensionsSnapshot(t, f, id) != before {
		t.Fatal("cooling period changed extensions")
	}
	if _, err = f.svc.AdminUndeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	if extensionsSnapshot(t, f, id) != before {
		t.Fatal("restore changed extensions")
	}
	if _, err = f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 1 {
		t.Fatal("purge", n, err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil || string(row.Extensions) != "{}" || row.ExtensionsSchemaVersion != nil || row.ExtensionsSchemaFingerprint != nil || row.ExtensionsRevision != 2 || row.ExtensionsUpdateTime == nil || !row.ExtensionsUpdateTime.Equal(*f.clock) {
		t.Fatal("purge must clear extensions and advance metadata", err)
	}
	after := extensionsSnapshot(t, f, id)
	if n, err := f.svc.PurgeDueUsers(ctx); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if extensionsSnapshot(t, f, id) != after {
		t.Fatal("repeated purge changed metadata")
	}
}

func TestUserExtensionsPurgeBoundaries(t *testing.T) {
	for _, mode := range []string{"disabled", "mismatch", "max", "neverused", "used_empty"} {
		t.Run(mode, func(t *testing.T) {
			f, id := extensionsFixture(t)
			ctx := context.Background()
			z := int64(0)
			want := int64(2)
			if mode != "neverused" {
				v, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-boundary"}`), ExpectedRevision: &z}, meta1)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "used_empty" {
					if _, err = f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":null}`), ExpectedRevision: &v.Revision}, meta1); err != nil {
						t.Fatal(err)
					}
					want = 3
				}
			} else {
				want = 0
			}
			if mode == "max" {
				mustExec(t, f, `UPDATE user_account SET extensions_revision=$2 WHERE id=$1`, id, int64(math.MaxInt64))
				want = math.MaxInt64
			}
			if mode == "disabled" {
				newServiceWith(t, f, func(d *user.Deps) { d.UserExtensions = nil })
			}
			if mode == "mismatch" {
				newServiceWith(t, f, func(d *user.Deps) {
					d.UserExtensions = &user.ExtensionsConfig{SchemaVersion: "v2", Schema: []byte(extensionsSchema)}
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
			if err != nil || string(row.Extensions) != "{}" || row.ExtensionsSchemaVersion != nil || row.ExtensionsSchemaFingerprint != nil || row.ExtensionsRevision != want {
				t.Fatal("boundary purge metadata", err)
			}
			if mode == "neverused" {
				if row.ExtensionsUpdateTime != nil {
					t.Fatal("unused metadata initialized")
				}
			} else if row.ExtensionsUpdateTime == nil || !row.ExtensionsUpdateTime.Equal(*f.clock) {
				t.Fatal("cleanup time missing")
			}
		})
	}
}

type extensionsRollbackAnonymizer struct {
	t    *testing.T
	seen bool
}

func (a *extensionsRollbackAnonymizer) Name() string     { return "synthetic_extensions_host" }
func (a *extensionsRollbackAnonymizer) Tables() []string { return nil }
func (a *extensionsRollbackAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, id string) error {
	var empty bool
	if err := tx.QueryRow(ctx, `SELECT extensions='{}'::jsonb AND extensions_schema_version IS NULL AND extensions_schema_fingerprint IS NULL AND extensions_revision=2 AND state=$2 FROM user_account WHERE id=$1`, id, enum.UserDeleted).Scan(&empty); err != nil {
		a.t.Fatal(err)
	}
	if !empty {
		a.t.Error("host callback must see cleared extensions in same transaction")
	}
	a.seen = true
	return errors.New("synthetic host down")
}
func TestUserExtensionsPurgeRollback(t *testing.T) {
	f, id := extensionsFixture(t)
	ctx := context.Background()
	z := int64(0)
	if _, err := f.svc.PatchExtensions(ctx, id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-rollback-private"}`), ExpectedRevision: &z}, meta1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, id, meta1); err != nil {
		t.Fatal(err)
	}
	before, err := f.repo.Q().GetUserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	a := &extensionsRollbackAnonymizer{t: t}
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
