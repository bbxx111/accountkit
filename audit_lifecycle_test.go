package accountkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

// Extra lifecycle/retention methods deliberately resemble host adapters. Auth
// must not discover or call them through incidental interface assertions.
type hostAudit struct {
	audit.Memory
	start, flush, close, delete atomic.Int32
	query                       audit.Query
}

func (h *hostAudit) Start(context.Context)       { h.start.Add(1) }
func (h *hostAudit) Flush(context.Context) error { h.flush.Add(1); return nil }
func (h *hostAudit) Close()                      { h.close.Add(1) }
func (h *hostAudit) DeleteOlderThan(context.Context, time.Time) (int64, error) {
	h.delete.Add(1)
	return 0, errors.New("host-private-retention-error")
}
func (h *hostAudit) ListByResource(_ context.Context, q audit.Query) (audit.Page, error) {
	h.query = q
	page := audit.Page{}
	for _, e := range h.Events() {
		for _, ref := range append(append([]audit.Resource(nil), e.Targets...), e.Context.RelatedResources...) {
			if e.Source == q.Source && ref.Namespace == q.Resource.Namespace && ref.Type == q.Resource.Type && ref.ID == q.Resource.ID {
				page.Records = append(page.Records, audit.RecordView{Event: e, CreateTime: time.Now()})
				break
			}
		}
	}
	sort.Slice(page.Records, func(i, j int) bool {
		if page.Records[i].OccurTime.Equal(page.Records[j].OccurTime) {
			return bytes.Compare(page.Records[i].ID[:], page.Records[j].ID[:]) < 0
		}
		return page.Records[i].OccurTime.Before(page.Records[j].OccurTime)
	})
	return page, nil
}
func assertHostAuditUntouched(t *testing.T, h *hostAudit) {
	t.Helper()
	if h.start.Load() != 0 || h.flush.Load() != 0 || h.close.Load() != 0 || h.delete.Load() != 0 {
		t.Fatalf("library took host component ownership: start=%d flush=%d close=%d delete=%d", h.start.Load(), h.flush.Load(), h.close.Load(), h.delete.Load())
	}
}

func auditAdminRequest(a *accountkit.Auth, uid string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Use(stubVerifier{}.Middleware())
	r.Mount("/", a.AdminHandler())
	req := httptest.NewRequest(http.MethodGet, "/users/"+uid+"/auditEvents", nil)
	req.Header.Set("X-Test-Admin", "https://admin.example.test|operator|Operator|operator")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAuditAssemblyModes(t *testing.T) {
	for _, mode := range []string{"local", "external_pair", "write_only"} {
		t.Run(mode, func(t *testing.T) {
			base, pool, sent := integrationInstance(t, minimal(), testRedis(t))
			d := base.DepsForTest()
			d.AdminVerifier, d.AdminPrincipal = stubVerifier{}, stubPrincipalFrom
			host := &hostAudit{}
			d.Audit = nil
			if mode != "local" {
				d.Audit = host
				if mode == "external_pair" {
					d.AuditReader = host
				}
			}
			a, err := accountkit.New(base.Config(), d)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			ctx := context.Background()
			if err := a.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			const phone = "+8613812340077"
			challenge, err := a.Users().SendSignInCode(ctx, enum.IdentityPhone, phone, user.Meta{IP: "127.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Users().SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityPhone, Target: phone, CodeID: challenge.CodeID, Code: sent.code(phone)}, user.Device{ID: "audit-assembly"}, user.Meta{})
			if err != nil || result.Tokens == nil {
				t.Fatalf("%s sign in: %+v %v", mode, result, err)
			}
			uid := result.Tokens.UserID
			identities, err := a.Users().ListIdentities(ctx, uid)
			if err != nil || len(identities) != 1 {
				t.Fatalf("identities: %+v %v", identities, err)
			}
			if p, err := a.Users().Authenticate(ctx, result.Tokens.AccessToken); err != nil || p.UserID != uid {
				t.Fatalf("%s authentication: %+v %v", mode, p, err)
			}
			admin := user.Admin{Issuer: "https://admin.example.test", Subject: "operator"}
			if revealed, err := a.Users().RevealIdentity(ctx, admin, uid, identities[0].ID, user.Meta{}); err != nil || revealed.Subject != phone {
				t.Fatalf("%s authenticated operation: %+v %v", mode, revealed, err)
			}
			a.Close() // default worker flushes; external writer remains usable.
			rec := auditAdminRequest(a, uid)
			if mode == "write_only" {
				if rec.Code != 503 || !strings.Contains(rec.Body.String(), "AUDIT_QUERY_NOT_CONFIGURED") {
					t.Fatalf("write-only query must fail explicitly: %d %s", rec.Code, rec.Body)
				}
			} else if rec.Code != 200 || !strings.Contains(rec.Body.String(), "IDENTITY_REVEALED") {
				t.Fatalf("%s query: %d %s", mode, rec.Code, rec.Body)
			}
			var localReveals int
			if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_event WHERE action='IDENTITY_REVEALED'").Scan(&localReveals); err != nil {
				t.Fatal(err)
			}
			if mode == "local" && localReveals != 1 || mode != "local" && localReveals != 0 {
				t.Fatalf("%s wrote to wrong backend: local=%d", mode, localReveals)
			}
			if mode != "local" && len(host.Events()) != 3 {
				t.Fatalf("external writer lost reveal: %d", len(host.Events()))
			}
			if mode == "external_pair" && (host.query.Source != a.Config().AuditSource || host.query.Resource.Namespace != a.Config().AuditNamespace || host.query.Resource.Type != "USER" || host.query.Resource.ID != uid) {
				t.Fatalf("wrong host query scope: %+v", host.query)
			}
			assertHostAuditUntouched(t, host)
		})
	}
}

func TestInjectedAuditLifecycleOwnedByHost(t *testing.T) {
	d := baseDeps(t)
	writer, reader := &hostAudit{}, &hostAudit{}
	d.Audit, d.AuditReader = writer, reader
	a, err := accountkit.New(externalAuditConfig(minimal()), d)
	if err != nil {
		t.Fatal(err)
	}
	assertHostAuditUntouched(t, writer)
	assertHostAuditUntouched(t, reader)
	ctx, cancel := context.WithCancel(context.Background())
	a.Start(ctx)
	a.Start(ctx)
	cancel()
	a.Close()
	a.Close()
	assertHostAuditUntouched(t, writer)
	assertHostAuditUntouched(t, reader)
	if err := d.Redis.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("host Redis closed: %v", err)
	}
	// The pool is intentionally unreachable: New/Start/Close must not acquire a connection.
	if d.Pool.Stat().TotalConns() != 0 {
		t.Fatal("New/Start/Close unexpectedly performed database I/O")
	}
	a.RecordAdminForbidden(httptest.NewRequest("GET", "/", nil), accountkit.AdminPrincipal{Issuer: "https://admin.example.test", Subject: "operator"})
	if len(writer.Events()) != 1 {
		t.Fatal("host recorder became unusable after Auth.Close")
	}
}

func TestAuditAssemblyNewDoesNotConnect(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "external"}[external], func(t *testing.T) {
			d := baseDeps(t)
			rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			t.Cleanup(func() { _ = rdb.Close() })
			d.Redis = rdb
			cfg := minimal()
			host := &hostAudit{}
			if external {
				cfg.AuditSource, cfg.AuditNamespace = "stable/source", "urn:stable:users"
				d.Audit, d.AuditReader = host, host
			}
			a, err := accountkit.New(cfg, d)
			if err != nil {
				t.Fatal(err)
			}
			a.Close()
			wantSource, wantNamespace := "accountkit/account", "accountkit/account/resources"
			if external {
				wantSource, wantNamespace = "stable/source", "urn:stable:users"
			}
			if got := a.Config(); got.AuditSource != wantSource || got.AuditNamespace != wantNamespace {
				t.Fatalf("audit scope changed/default missing: source=%s namespace=%s", got.AuditSource, got.AuditNamespace)
			}
			if rdb.PoolStats().TotalConns != 0 || d.Pool.Stat().TotalConns() != 0 {
				t.Fatal("New/empty Close connected to host storage")
			}
			if host.query.Source != "" {
				t.Fatal("New queried host reader")
			}
			assertHostAuditUntouched(t, host)
		})
	}
}

func TestAuditModeSwitchMaintainsOldLocalCopies(t *testing.T) {
	base, pool, _ := integrationInstance(t, minimal(), testRedis(t))
	d := base.DepsForTest()
	d.Audit = nil
	local, err := accountkit.New(base.Config(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(local.Close)
	f, _ := seedSyntheticAccount(t, pool, local.Config())
	admin := user.Admin{Issuer: "https://admin.example.test", Subject: "operator", Username: "private-name"}
	if _, err := local.Users().RevealIdentity(context.Background(), admin, f.UserID, f.IdentityID, user.Meta{IP: "203.0.113.4"}); err != nil {
		t.Fatal(err)
	}
	local.Close()
	before := snapshotSource(t, pool)
	host := &hostAudit{}
	d.Audit, d.AuditReader = host, host
	external, err := accountkit.New(local.Config(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(external.Close)
	if snapshotSource(t, pool) != before || len(host.Events()) != 0 {
		t.Fatal("mode switch changed or copied old local data")
	}
	ctx := context.Background()
	if _, err := external.Users().RevealIdentity(ctx, admin, f.UserID, f.IdentityID, user.Meta{IP: "203.0.113.4"}); err != nil {
		t.Fatal(err)
	}
	hostBefore, err := json.Marshal(host.Events())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := external.Users().AdminDeleteUser(ctx, admin, f.UserID, user.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE user_account SET purge_time=now()-interval '1 hour' WHERE id=$1", f.UserID); err != nil {
		t.Fatal(err)
	}
	if !external.RunMaintenanceOnce(ctx) {
		t.Fatal("external-mode maintenance did not acquire lock")
	}
	var state int
	var actorName, ip *string
	if err := pool.QueryRow(ctx, "SELECT state FROM user_account WHERE id=$1", f.UserID).Scan(&state); err != nil || state != 4 {
		t.Fatalf("old local account not purged: state=%d err=%v", state, err)
	}
	if err := pool.QueryRow(ctx, "SELECT actor->>'name',context->>'client_ip' FROM audit_event WHERE action='IDENTITY_REVEALED'").Scan(&actorName, &ip); err != nil || actorName != nil || ip != nil {
		t.Fatalf("old local copy retained privacy: name=%v ip=%v err=%v", actorName, ip, err)
	}
	// External privacy is host-owned: even a same-named delete method is not called.
	hostAfter, err := json.Marshal(host.Events()[:1])
	if err != nil || !bytes.Equal(hostBefore, hostAfter) {
		t.Fatalf("library mutated external record: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE audit_event SET occur_time=now()-interval '200 days'"); err != nil {
		t.Fatal(err)
	}
	if !external.RunMaintenanceOnce(ctx) {
		t.Fatal("second retention round did not acquire lock")
	}
	var left int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_event").Scan(&left); err != nil || left != 0 {
		t.Fatalf("old local retention stopped after mode switch: %d %v", left, err)
	}
	external.Close()
	assertHostAuditUntouched(t, host)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("host pool closed: %v", err)
	}
}

type failedAuditInserter struct{ timeout bool }

func (f failedAuditInserter) InsertBatch(ctx context.Context, es []audit.Event) (audit.BatchResult, error) {
	if f.timeout {
		<-ctx.Done()
	}
	return audit.BatchResult{Failed: len(es)}, errors.New("refresh_token=synthetic-private-host-secret")
}

func TestAuditFailureDoesNotChangeAuthentication(t *testing.T) {
	for _, mode := range []string{"full", "closed", "backend_error", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			base, pool, _ := integrationInstance(t, minimal(), testRedis(t))
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			pipe := audit.NewAsync(failedAuditInserter{timeout: mode == "timeout"}, audit.AsyncOptions{QueueSize: 1, BatchSize: 1, CloseTimeout: 20 * time.Millisecond, Logger: logger})
			d := base.DepsForTest()
			d.Audit, d.Logger = pipe, logger
			a, err := accountkit.New(base.Config(), d)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.Close)
			f, access := seedSyntheticAccount(t, pool, a.Config())
			if mode == "closed" {
				pipe.Close()
			}
			for i := range 2 {
				if p, err := a.Users().Authenticate(context.Background(), access); err != nil || p.UserID != f.UserID {
					t.Fatalf("audit failure changed authenticate: %+v %v", p, err)
				}
				if revealed, err := a.Users().RevealIdentity(context.Background(), user.Admin{Issuer: "https://admin.example.test", Subject: "operator"}, f.UserID, f.IdentityID, user.Meta{}); err != nil || revealed.Subject != f.Phone {
					t.Fatalf("audit failure changed reveal: %+v %v", revealed, err)
				}
				if mode == "backend_error" || mode == "timeout" {
					pipe.Start(context.Background())
					waitAuditFailure(t, pipe, uint64(i+1))
				}
			}
			a.Close() // injected pipeline remains host-owned.
			pipe.Close()
			st := pipe.Stats()
			if mode == "full" && (st.Dropped != 1 || st.Failed != 1) || mode == "closed" && st.Dropped != 2 || (mode == "backend_error" || mode == "timeout") && st.Failed != 2 {
				t.Fatalf("%s stats=%+v", mode, st)
			}
			if strings.Contains(logs.String(), "synthetic-private-host-secret") || strings.Contains(logs.String(), f.Phone) {
				t.Fatalf("audit failure leaked secret: %s", logs.String())
			}
		})
	}
}

func waitAuditFailure(t *testing.T, pipe *audit.Async, want uint64) {
	t.Helper()
	// Wait for the single queued batch before producing the next; no queue-full
	// loss is allowed to accidentally satisfy backend failure cases.
	deadline := time.Now().Add(2 * time.Second)
	for pipe.Stats().Failed < want {
		if time.Now().After(deadline) {
			t.Fatalf("backend did not finish: %+v", pipe.Stats())
		}
		time.Sleep(time.Millisecond)
	}
}
