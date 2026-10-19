package accountkit_test

import (
	"context"
	"errors"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

type emptyAuditReader struct{}

func (emptyAuditReader) ListByResource(context.Context, audit.Query) (audit.Page, error) {
	return audit.Page{}, nil
}
func TestAuditExternalConfigurationMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, source, namespace string
		writer, reader, valid   bool
	}{
		{"local", "", "", false, false, true}, {"reader_only", "source", "namespace", false, true, false}, {"writer_missing", "", "", true, false, false}, {"source_only", "source", "", true, false, false}, {"namespace_only", "", "namespace", true, false, false}, {"write_only", "source", "namespace", true, false, true}, {"external_pair", "source", "namespace", true, true, true}, {"explicit_defaults", "accountkit/account", "accountkit/account/resources", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimal()
			cfg.AuditSource = tc.source
			cfg.AuditNamespace = tc.namespace
			d := baseDeps(t)
			if tc.writer {
				d.Audit = audit.Noop{}
			}
			if tc.reader {
				d.AuditReader = emptyAuditReader{}
			}
			a, err := accountkit.New(cfg, d)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if a != nil {
				a.Close()
			}
		})
	}
}

// A single host connection must be returned on success and baseline rejection.
func TestMigrateAuditConfigurationSingleConnection(t *testing.T) {
	for _, mode := range []string{"fresh_repeat", "old_v1_empty", "old_v1_nonempty"} {
		t.Run(mode, func(t *testing.T) {
			a, base, _ := integrationInstance(t, minimal(), testRedis(t))
			cfg := base.Config()
			cfg.MaxConns = 1
			pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			d := baseDeps(t)
			d.Pool = pool
			d.Audit = audit.Noop{}
			single, err := accountkit.New(a.Config(), d)
			if err != nil {
				t.Fatal(err)
			}
			defer single.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode != "fresh_repeat" {
				initial, err := os.ReadFile("migrations/testdata/legacy_integrated_v1.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, "CREATE SCHEMA "+single.Config().Schema); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, string(initial)); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, "CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL);INSERT INTO schema_migrations VALUES(1,false)"); err != nil {
					t.Fatal(err)
				}
				if mode == "old_v1_nonempty" {
					if _, err := pool.Exec(ctx, "INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000001',1,1,1)"); err != nil {
						t.Fatal(err)
					}
				}
				before := snapshotSourceWithFlowTable(t, pool, "identity_flow")
				for range 2 {
					if err := single.Migrate(ctx); !errors.Is(err, migrations.ErrIncompatibleBaseline) {
						t.Errorf("old baseline accepted or changed: %v", err)
					}
				}
				if snapshotSourceWithFlowTable(t, pool, "identity_flow") != before {
					t.Fatal("rejected old schema changed data/version")
				}
			} else {
				if err := single.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				seedFormatFixture(t, pool, single.Config())
				before := snapshotSource(t, pool)
				// source belongs to runtime query scope, not the initial database definition.
				other := single.Config()
				other.AuditSource = "other/source"
				second, err := accountkit.New(other, d)
				if err != nil {
					t.Fatal(err)
				}
				defer second.Close()
				for range 2 {
					if err := second.Migrate(ctx); err != nil {
						t.Fatalf("one connection migration blocked: %v", err)
					}
				}
				if snapshotSource(t, pool) != before {
					t.Fatal("repeat root Migrate changed data")
				}
				var version int
				var dirty bool
				if err := pool.QueryRow(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 2 || dirty {
					t.Fatalf("version=%d dirty=%v err=%v", version, dirty, err)
				}
			}
			if pool.Stat().AcquiredConns() != 0 {
				t.Fatal("Migrate leaked host connection")
			}
			if err := pool.Ping(ctx); err != nil {
				t.Fatalf("host connection not reusable: %v", err)
			}
		})
	}
}
