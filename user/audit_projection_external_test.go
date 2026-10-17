package user_test

import (
	"context"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
)

type testAuditMemory struct{ audit.Memory }

func (m *testAuditMemory) Events() []audit.AuthenticationEvent {
	raw := m.Memory.Events()
	out := make([]audit.AuthenticationEvent, len(raw))
	for i, e := range raw {
		var err error
		out[i], err = authentication.Projection(e)
		if err != nil {
			panic(err)
		}
	}
	return out
}
func (m *testAuditMemory) RawEvents() []audit.Event { return m.Memory.Events() }

func localAuditOptions(pool *pgxpool.Pool) audit.AuthenticationOptions {
	schema := strings.Split(pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]
	return audit.AuthenticationOptions{Source: "accountkit/" + schema, Namespace: "accountkit/" + schema + "/resources"}
}
func seedAuditInput(t *testing.T, pool *pgxpool.Pool, in audit.AuthenticationEvent) audit.Event {
	t.Helper()
	e, err := authentication.Map(in, localAuditOptions(pool))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewStore(pool).InsertBatch(context.Background(), []audit.Event{e}); err != nil {
		t.Fatal(err)
	}
	return e
}
