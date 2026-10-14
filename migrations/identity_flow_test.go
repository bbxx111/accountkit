package migrations

import (
	"context"
	"errors"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestIdentityFlowMigration(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err = c.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	q := `INSERT INTO identity_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,create_time,update_time,expire_time) VALUES($1,$2,1,'policy',decode('1234','hex'),1,now(),now(),now()+interval '5 minutes')`
	if _, err = c.Exec(ctx, q, "f_0000000000001", "digest"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Exec(ctx, q, "f_0000000000002", "digest"); err == nil {
		t.Fatal("duplicate token accepted")
	}
	if _, err = c.Exec(ctx, q, "u_0000000000003", "other"); err == nil {
		t.Fatal("invalid flow id accepted")
	}
	var count int
	if err = c.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname=$1 AND tablename='identity_flow'`, schema).Scan(&count); err != nil || count < 5 {
		t.Fatalf("indexes %d %v", count, err)
	}
}

func TestIdentityFlowCASAndCleanup(t *testing.T) {
	cfg, schema := safetyDB(t)
	ctx := context.Background()
	if err := Up(ctx, cfg, schema); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err = c.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	q := db.New(c)
	now := time.Now().UTC()
	flow, err := q.CreateIdentityFlow(ctx, db.CreateIdentityFlowParams{ID: "f_0000000000001", TokenDigest: "test-digest", State: enum.FlowPending, PolicyFingerprint: "policy", Payload: []byte{1}, CipherVersion: 1, ProofUserIds: []string{"u_0000000000001"}, CreateTime: now, ExpireTime: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	update := db.UpdateIdentityFlowParams{ID: flow.ID, Revision: flow.Revision, State: enum.FlowVerified, Payload: []byte{2}, CipherVersion: 1, ProofUserIds: flow.ProofUserIds, Now: now}
	flow, err = q.UpdateIdentityFlow(ctx, update)
	if err != nil || flow.Revision != 2 {
		t.Fatalf("update %v %v", flow, err)
	}
	if _, err = q.UpdateIdentityFlow(ctx, update); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stale CAS: %v", err)
	}
	complete := db.CompleteIdentityFlowParams{ID: flow.ID, Revision: flow.Revision, Payload: []byte{3}, CipherVersion: 1, ProofUserIds: flow.ProofUserIds, UserID: "u_0000000000002", SessionID: "s_0000000000001", Now: now}
	flow, err = q.CompleteIdentityFlow(ctx, complete)
	if err != nil || flow.State != enum.FlowCompleted || flow.Revision != 3 {
		t.Fatalf("complete %v %v", flow, err)
	}
	complete.Revision = flow.Revision
	if _, err = q.CompleteIdentityFlow(ctx, complete); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second completion %v", err)
	}
	versions, err := q.RequiredIdentityFlowCipherVersions(ctx, now)
	if err != nil || len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("versions %v %v", versions, err)
	}
	n, err := q.DeleteIdentityFlowsForUser(ctx, "u_0000000000001")
	if err != nil || n != 1 {
		t.Fatalf("proof owner cleanup %d %v", n, err)
	}
}
