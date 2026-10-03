package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestUnsafeResetRequiresConfirmation(t *testing.T) {
	for _, tc := range []struct{ schema, confirm string }{{"auth", ""}, {"auth", "other"}, {"invalid-schema", "invalid-schema"}} {
		t.Run(tc.schema+"/"+tc.confirm, func(t *testing.T) {
			// nil connection proves rejection happens before database access.
			if err := UnsafeReset(context.Background(), nil, tc.schema, UnsafeResetOptions{ConfirmSchema: tc.confirm}); err == nil {
				t.Fatal("unconfirmed reset accepted")
			}
		})
	}
	if !errors.Is(Down(context.Background(), nil, "auth"), ErrDestructiveOperation) {
		t.Fatal("Down must return sentinel without I/O")
	}
}
func TestUnsafeResetOnlyTargetSchema(t *testing.T) {
	cfg, a := safetyDB(t)
	_, b := safetyDB(t)
	ctx := context.Background()
	for _, s := range []string{a, b} {
		if err := Up(ctx, cfg, s); err != nil {
			t.Fatal(err)
		}
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "INSERT INTO "+b+".user_account(id,state) VALUES('u_0000000000001',1)"); err != nil {
		t.Fatal(err)
	}
	if err := UnsafeReset(ctx, cfg, a, UnsafeResetOptions{ConfirmSchema: a}); err != nil {
		t.Fatal(err)
	}
	var objects, schemas, users int
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=$1 AND tablename='user_account'", a).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", a).Scan(&schemas); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRow(ctx, "SELECT count(*) FROM "+b+".user_account").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if objects != 0 || schemas != 1 || users != 1 {
		t.Fatalf("reset isolation: tables=%d schema=%d other-users=%d", objects, schemas, users)
	}
}
