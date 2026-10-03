package accountkit_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recoveryState struct {
	Fixture          sourceFixture
	Access, Snapshot string
}

// TestRecoveryFixture is an operational fixture, invoked in each phase by verify-recovery.sh.
// It never cleans up a database: the caller supplies separate, disposable empty databases.
func TestRecoveryFixture(t *testing.T) {
	mode := os.Getenv("ACCOUNTKIT_RECOVERY_MODE")
	if mode == "" {
		t.Skip("executed separately by scripts/verify-recovery.sh")
	}
	if mode != "seed" && mode != "verify" && mode != "source-unchanged" {
		t.Fatal("unknown recovery fixture mode")
	}
	dsn := os.Getenv("ACCOUNTKIT_RECOVERY_DSN")
	stateFile := os.Getenv("ACCOUNTKIT_RECOVERY_STATE")
	if dsn == "" || stateFile == "" {
		t.Fatal("recovery DSN and state file are required")
	}
	cfg := minimal()
	cfg.Schema = "accountkit_recovery"
	pc, err := accountkit.PoolConfig(dsn, cfg.Schema)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	captured := &captureSender{}
	a, err := accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: testRedis(t), SMSSender: captured, EmailSender: captured, Audit: audit.Noop{}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if mode == "seed" {
		f, access := seedSourceFixture(t, pool, a.Config())
		b, err := json.Marshal(recoveryState{f, access, snapshotSource(t, pool)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stateFile, b, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var state recoveryState
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	if got := snapshotSource(t, pool); got != state.Snapshot {
		t.Fatal("restored data/version differs from the source snapshot")
	}
	if mode == "source-unchanged" {
		return
	}
	for range 2 {
		if err := a.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if snapshotSource(t, pool) != state.Snapshot {
		t.Fatal("Migrate changed restored data")
	}
	principal, err := a.Users().Authenticate(ctx, state.Access)
	if err != nil || principal.UserID != state.Fixture.UserID {
		t.Fatalf("restored access unusable: %v", err)
	}
	meta := user.Meta{IP: "127.0.0.1"}
	identity, err := a.Users().RevealIdentity(ctx, user.Admin{}, state.Fixture.UserID, state.Fixture.IdentityID, meta)
	if err != nil || identity.Subject != state.Fixture.Phone {
		t.Fatalf("restored identity not decryptable: %v", err)
	}
	tok, err := a.Users().Refresh(ctx, state.Fixture.RefreshToken, meta)
	if err != nil || tok.UserID != state.Fixture.UserID || tok.Scope != "user" || tok.RefreshToken == state.Fixture.RefreshToken {
		t.Fatalf("restored refresh unusable: %v", err)
	}
}
