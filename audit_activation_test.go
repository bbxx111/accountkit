package accountkit_test

import (
	"context"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/jackc/pgx/v5/pgxpool"
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

// Host connections must be returned after a successful repeat Migrate at minimum capacity.
func TestMigrateAuditConfigurationMinimumPoolCapacity(t *testing.T) {
	a, base, _ := integrationInstance(t, minimal(), testRedis(t))
	cfg := base.Config()
	cfg.MaxConns = 2
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
	if err := single.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	seedSyntheticAccount(t, pool, single.Config())
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
			t.Fatalf("minimum-capacity migration blocked: %v", err)
		}
	}
	if snapshotSource(t, pool) != before {
		t.Fatal("repeat root Migrate changed data")
	}
	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 1 || dirty {
		t.Fatalf("version=%d dirty=%v err=%v", version, dirty, err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("Migrate leaked host connection")
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("host connection not reusable: %v", err)
	}
}
