package authserver_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	authserver "github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/jackc/pgx/v5"
)

type failingHostAnonymizer struct{}

func (failingHostAnonymizer) Name() string     { return "host_note" }
func (failingHostAnonymizer) Tables() []string { return []string{"host_note"} }
func (failingHostAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, "UPDATE host_note SET body='changed' WHERE user_id=$1", id); err != nil {
		return err
	}
	return errors.New("fixture failure after host write")
}

func TestHostContracts(t *testing.T) {
	rdb := testRedis(t)
	a, pool, sent := integrationInstance(t, minimal(), rdb)
	f, _ := seedSourceFixture(t, pool, a.Config())
	ctx := context.Background()
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	a.AdminHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/users", nil))
	if recorder.Code != 503 {
		t.Fatalf("admin without verifier = %d", recorder.Code)
	}
	// Consumer usability is covered by TestSourceDatabaseTakeover with no admin dependencies.
	for _, q := range []string{
		"CREATE TABLE host_note(user_id text PRIMARY KEY, body text)",
		"INSERT INTO host_note VALUES('u_0000000000001','original')",
		"UPDATE user_account SET state=3,delete_time=now()-interval '20 days',purge_time=now()-interval '1 day'",
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotSource(t, pool)
	withHost, err := authserver.New(a.Config(), authserver.Deps{Pool: pool, Redis: rdb, SMSSender: sent, EmailSender: sent, Audit: audit.Noop{}, Anonymizers: []anonymize.Anonymizer{failingHostAnonymizer{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer withHost.Close()
	if n, err := withHost.Users().PurgeDueUsers(ctx); err == nil || n != 0 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if snapshotSource(t, pool) != before {
		t.Fatal("authentication changes escaped rollback")
	}
	var body string
	if err := pool.QueryRow(ctx, "SELECT body FROM host_note WHERE user_id=$1", f.UserID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "original" {
		t.Fatalf("host changes escaped rollback: %s", body)
	}
}
