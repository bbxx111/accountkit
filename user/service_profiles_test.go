package user

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// profilesTrace observes queries sent to real PostgreSQL, including their batch input.
type profilesTrace struct {
	mu    sync.Mutex
	calls int
	args  []any
}

func (p *profilesTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.args = append([]any(nil), data.Args...)
	return ctx
}
func (*profilesTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (p *profilesTrace) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls, p.args = 0, nil
}
func (p *profilesTrace) snapshot() (int, []any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]any(nil), p.args...)
}

func profilesPool(t *testing.T, trace *profilesTrace) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	suffix, err := ids.New(ids.User)
	if err != nil {
		t.Fatal(err)
	}
	schema := "profiles_" + strings.TrimPrefix(suffix, "u_")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	if err := migrations.Up(context.Background(), cfg.ConnConfig, schema); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		pool.Close()
	})
	return pool
}

func TestBatchPublicProfiles(t *testing.T) {
	t.Run("empty input requires no database", func(t *testing.T) {
		svc := &Service{}
		for _, input := range [][]string{nil, {}} {
			got, err := svc.BatchPublicProfiles(context.Background(), input)
			if err != nil || got == nil || len(got) != 0 {
				t.Fatalf("empty input: got=%v err=%v", got, err)
			}
		}
	})
	t.Run("invalid IDs rejected before database access", func(t *testing.T) {
		svc := &Service{}
		for _, invalid := range []string{"", "u_short", "s_0000000000001", "u_000000000000I", "u_000000000000o", "users/u_0000000000001"} {
			got, err := svc.BatchPublicProfiles(context.Background(), []string{"u_0000000000001", invalid})
			if !errors.Is(err, ErrInvalidArgument) || got != nil {
				t.Fatalf("invalid ID %q: got=%v err=%v", invalid, got, err)
			}
		}
	})
	t.Run("public result excludes private fields", func(t *testing.T) {
		typ := reflect.TypeOf(PublicProfile{})
		allowed := map[string]reflect.Type{"ID": reflect.TypeOf(""), "DisplayName": reflect.TypeOf(""), "State": reflect.TypeOf(enum.UserActive)}
		if typ.NumField() != len(allowed) {
			t.Fatalf("public profile includes extra fields: %v", typ)
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if want, ok := allowed[field.Name]; !ok || field.Type != want || !field.IsExported() {
				t.Fatalf("unexpected public profile field: %+v", field)
			}
		}
	})
	t.Run("real database returns only requested public profiles in one query", func(t *testing.T) {
		trace := &profilesTrace{}
		pool := profilesPool(t, trace)
		svc := &Service{d: Deps{Repo: NewRepo(pool)}}
		ctx := context.Background()
		records := []struct {
			id       string
			state    enum.UserState
			name     *string
			wantName string
		}{
			{"u_0000000000001", enum.UserActive, profilesName("Active name"), "Active name"},
			{"u_0000000000002", enum.UserPendingDeletion, profilesName("Pending private name"), ""},
			{"u_0000000000003", enum.UserDeleted, profilesName("Deleted private name"), ""},
			{"u_0000000000004", enum.UserFrozen, profilesName("Frozen name"), "Frozen name"},
			{"u_0000000000005", enum.UserActive, nil, ""},
		}
		for _, record := range records {
			if _, err := pool.Exec(ctx, "INSERT INTO user_account (id, state, display_name) VALUES ($1, $2, $3)", record.id, record.state, record.name); err != nil {
				t.Fatal(err)
			}
		}
		const unknownID = "u_0000000000006"
		input := []string{records[0].id, records[1].id, records[0].id, records[2].id, unknownID, records[3].id, records[4].id, unknownID}
		trace.reset()
		got, err := svc.BatchPublicProfiles(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(records) {
			t.Fatalf("profile count: got %d want %d", len(got), len(records))
		}
		if _, exists := got[unknownID]; exists {
			t.Fatal("unknown profile leaked")
		}
		for _, record := range records {
			profile, exists := got[record.id]
			if !exists || profile.ID != record.id || profile.State != record.state || profile.DisplayName != record.wantName {
				t.Fatalf("profile for %s: got=%+v exists=%v", record.id, profile, exists)
			}
		}
		calls, args := trace.snapshot()
		if calls != 1 {
			t.Fatalf("batch sent %d queries to PostgreSQL; want one", calls)
		}
		if len(args) != 1 {
			t.Fatalf("batch query arguments: %v", args)
		}
		queried, ok := args[0].([]string)
		if !ok {
			t.Fatalf("batch argument type: %T", args[0])
		}
		wantIDs := map[string]bool{records[0].id: true, records[1].id: true, records[2].id: true, records[3].id: true, records[4].id: true, unknownID: true}
		if len(queried) != len(wantIDs) {
			t.Fatalf("batch argument was not deduplicated: %v", queried)
		}
		for _, id := range queried {
			if !wantIDs[id] {
				t.Fatalf("unexpected or repeated batch ID %q", id)
			}
			delete(wantIDs, id)
		}
		t.Logf("real PostgreSQL batch: queries=%d, deduplicated IDs=%d", calls, len(queried))

		trace.reset()
		unknown, err := svc.BatchPublicProfiles(ctx, []string{unknownID})
		if err != nil || unknown == nil || len(unknown) != 0 {
			t.Fatalf("unknown input: got=%v err=%v", unknown, err)
		}
		if calls, _ := trace.snapshot(); calls != 1 {
			t.Fatalf("unknown input sent %d queries", calls)
		}

		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		failed, err := svc.BatchPublicProfiles(cancelled, []string{records[0].id})
		if !errors.Is(err, context.Canceled) || failed != nil {
			t.Fatalf("database cancellation: got=%v err=%v", failed, err)
		}
	})
}

func profilesName(name string) *string { return &name }

var _ pgx.QueryTracer = (*profilesTrace)(nil)
