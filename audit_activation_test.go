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
func TestMigrateAuditConfigurationSingleConnection(t *testing.T) {
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
	seedFormatFixture(t, pool, single.Config())
	prepareFormatFixtureUpgrade(t, pool, single.Config())
	wrong := single.Config()
	wrong.AuditSource = "other/source"
	mismatch, err := accountkit.New(wrong, d)
	if err != nil {
		t.Fatal(err)
	}
	defer mismatch.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mismatch.Migrate(ctx); err == nil {
		t.Fatal("staged configuration mismatch accepted")
	}
	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 1 || dirty {
		t.Fatalf("precheck changed migration: %d %v %v", version, dirty, err)
	}
	if err := single.Migrate(ctx); err != nil {
		t.Fatalf("one connection migration blocked: %v", err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("precheck leaked connection")
	}
}
