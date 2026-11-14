package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResolveIdentityServerTimeout(t *testing.T) {
	for _, parameter := range []string{"statement_timeout", "lock_timeout", "transaction_timeout"} {
		t.Run(parameter, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			cfg := f.pool.Config()
			cfg.ConnConfig.RuntimeParams[parameter] = "100ms"
			pool, e := pgxpool.NewWithConfig(ctx, cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer pool.Close()
			f.deps.Repo = user.NewRepo(pool)
			memory := &audit.Memory{}
			f.deps.Audit = memory
			f.svc, e = user.NewService(f.deps)
			if e != nil {
				t.Fatal(e)
			}
			tx, e := f.pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(ctx)
			if _, e = tx.Exec(ctx, "LOCK TABLE identity IN ACCESS EXCLUSIVE MODE"); e != nil {
				t.Fatal(e)
			}
			input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
			start := time.Now()
			_, e = f.svc.ResolveIdentity(ctx, input)
			if !errors.Is(e, user.ErrUnavailable) {
				t.Errorf("server timeout: got %v, want ErrUnavailable", e)
			}
			if time.Since(start) > time.Second {
				t.Fatal("server timeout did not fire before Go deadline")
			}
			events := memory.Events()
			if len(events) != 1 {
				t.Fatalf("events %d", len(events))
			}
			var data map[string]string
			if e = json.Unmarshal(events[0].Data, &data); e != nil {
				t.Fatal(e)
			}
			if len(data) != 2 || data["kind"] != "PHONE" || data["outcome"] != "unavailable" || len(events[0].Targets) != 0 || events[0].Result == nil || *events[0].Result != enum.ResultFailure {
				t.Fatalf("server timeout audit outcome: %q", data["outcome"])
			}

		})
	}
}

func TestResolveIdentityAuditFailureDataAndPrivacy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	if _, e := f.pool.Exec(ctx, "UPDATE user_account SET state=99,display_name='private-profile',avatar_url='https://private.invalid/avatar' WHERE id=$1", signed.UserID); e != nil {
		t.Fatal(e)
	}
	memory := &audit.Memory{}
	f.deps.Audit = memory
	var e error
	f.svc, e = user.NewService(f.deps)
	if e != nil {
		t.Fatal(e)
	}
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: "13812341234", Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business", Name: "private-actor"}, Meta: meta1}
	if _, e = f.svc.ResolveIdentity(ctx, input); e == nil || errors.Is(e, user.ErrUnavailable) {
		t.Fatalf("internal: %v", e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = f.svc.ResolveIdentity(canceled, input); !errors.Is(e, user.ErrUnavailable) {
		t.Fatalf("unavailable: %v", e)
	}
	// A missing query relation remains an internal SQL defect, not an outage.
	cfg := f.pool.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	f.deps.Repo = user.NewRepo(pool)
	f.svc, e = user.NewService(f.deps)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.ResolveIdentity(ctx, input); e == nil || errors.Is(e, user.ErrUnavailable) || e.Error() != "user: identity resolution query failed" {
		t.Fatalf("SQL defect: %v", e)
	}
	events := memory.Events()
	if len(events) != 3 {
		t.Fatalf("events %d", len(events))
	}
	digest, _ := f.dig.Digest(phone1)
	for i, event := range events {
		var data map[string]string
		if e = json.Unmarshal(event.Data, &data); e != nil {
			t.Fatal(e)
		}
		want := []string{"internal", "unavailable", "internal"}[i]
		if len(data) != 2 || data["kind"] != "PHONE" || data["outcome"] != want || event.Action != "IDENTITY_RESOLVE" || event.Source == "" || event.Actor.Name != "" || event.Actor.ID != "business" || event.Context.RequestID != meta1.RequestID || len(event.Targets) != 0 || event.Result == nil || *event.Result != enum.ResultFailure {
			t.Fatalf("fault audit: %+v %s", event, event.Data)
		}
		wire, _ := json.Marshal(event)
		for _, secret := range []string{input.Subject, phone1, digest, "private-profile", "https://private.invalid/avatar", "private-actor"} {
			if bytes.Contains(wire, []byte(secret)) {
				t.Fatalf("audit contains private field %q", secret)
			}
		}
	}
}

func TestResolveIdentityConnectionAcquisitionBudget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	held, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Release()
	f.deps.Repo = user.NewRepo(pool)
	f.svc, e = user.NewService(f.deps)
	if e != nil {
		t.Fatal(e)
	}
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	limited, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e = f.svc.ResolveIdentity(limited, input); !errors.Is(e, user.ErrUnavailable) || time.Since(start) > time.Second {
		t.Fatalf("acquisition cancellation: %v %v", time.Since(start), e)
	}
	start = time.Now()
	_, e = f.svc.ResolveIdentity(ctx, input)
	elapsed := time.Since(start)
	if !errors.Is(e, user.ErrUnavailable) || elapsed < 2500*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("acquisition budget: %v %v", elapsed, e)
	}
}

func TestResolveIdentity(t *testing.T) {
	f := newFixture(t)
	actor := audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}
	for _, tc := range []struct {
		kind                enum.IdentityKind
		subject, equivalent string
	}{{enum.IdentityPhone, phone1, "13812341234"}, {enum.IdentityEmail, "Resolve@Example.COM", "resolve@example.com"}} {
		signed := f.signIn(t, tc.kind, tc.subject, dev1)
		for _, state := range []enum.UserState{enum.UserActive, enum.UserFrozen, enum.UserPendingDeletion} {
			if _, err := f.pool.Exec(context.Background(), "UPDATE user_account SET state=$2 WHERE id=$1", signed.UserID, state); err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.ResolveIdentity(context.Background(), user.ResolveIdentityInput{Kind: tc.kind, Subject: tc.equivalent, Actor: actor})
			if err != nil || got.UserID != signed.UserID || got.State != state || got.DisplayName != "" || got.AvatarURL != "" {
				t.Fatalf("got %+v err %v", got, err)
			}
		}
	}
	_, err := f.svc.ResolveIdentity(context.Background(), user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone2, Actor: actor})
	if !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	for _, input := range []user.ResolveIdentityInput{{Kind: "UNKNOWN", Subject: phone1, Actor: actor}, {Kind: enum.IdentityPhone, Subject: phone1}} {
		if _, err := f.svc.ResolveIdentity(context.Background(), input); !errors.Is(err, user.ErrInvalidArgument) {
			t.Fatalf("invalid: %v", err)
		}
	}
}

func TestResolveIdentityAudit(t *testing.T) {
	f := newFixture(t)
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	mem := &audit.Memory{}
	f.deps.Audit = mem
	var err error
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business", Name: "private-name"}, Meta: meta1}
	for _, subject := range []string{phone1, phone2} {
		input.Subject = subject
		f.svc.ResolveIdentity(context.Background(), input)
	}
	events := mem.Events()
	if len(events) != 2 {
		t.Fatalf("events %d", len(events))
	}
	for i, e := range events {
		wire, _ := json.Marshal(e)
		if bytes.Contains(wire, []byte(phone1)) || bytes.Contains(wire, []byte("private-name")) || e.Actor.Name != "" || e.Action != "IDENTITY_RESOLVE" || e.Context.RequestID != meta1.RequestID {
			t.Fatalf("unsafe event %s", wire)
		}
		if i == 0 && (len(e.Targets) != 1 || e.Targets[0].ID != signed.UserID || *e.Result != enum.ResultSuccess) {
			t.Fatal("success audit")
		}
		if i == 1 && (len(e.Targets) != 0 || *e.Result != enum.ResultFailure) {
			t.Fatal("miss audit")
		}
	}
}

func TestResolveIdentityRotationInvariantsAndReadOnly(t *testing.T) {
	f := newFixture(t)
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	rotated, _ := pii.NewDigester(map[uint16][]byte{1: bytes.Repeat([]byte{1}, 32), 2: bytes.Repeat([]byte{8}, 32)}, 2)
	f.deps.Digester = rotated
	f.deps.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	f.deps.RequiredIdentityKinds = []enum.IdentityKind{enum.IdentityEmail}
	f.deps.SMS = nil
	var err error
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	read := func() (user.ResolvedIdentity, error) { return f.svc.ResolveIdentity(ctx, input) }
	snapshot := func() string {
		var v string
		if e := f.pool.QueryRow(ctx, "SELECT jsonb_build_array((SELECT jsonb_agg(t) FROM user_account t),(SELECT jsonb_agg(t) FROM identity t),(SELECT jsonb_agg(t) FROM session t))::text").Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	before := snapshot()
	got, err := read()
	if err != nil || got.UserID != signed.UserID {
		t.Fatalf("rotation: %+v %v", got, err)
	}
	if snapshot() != before {
		t.Fatal("resolve wrote account data")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := f.pool.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, state := range []enum.UserState{enum.UserDeleted, enum.UserState(99)} {
		exec("UPDATE user_account SET state=$1 WHERE id=$2", state, signed.UserID)
		if _, e := read(); e == nil || errors.Is(e, user.ErrNotFound) || errors.Is(e, user.ErrUnavailable) {
			t.Fatalf("invalid state: %v", e)
		}
	}
	exec("UPDATE user_account SET state=$1 WHERE id=$2", enum.UserActive, signed.UserID)
	exec("UPDATE identity SET user_id='u_0000000000000' WHERE user_id=$1", signed.UserID)
	if _, e := read(); e == nil || errors.Is(e, user.ErrUnavailable) {
		t.Fatalf("dangling: %v", e)
	}
	exec("UPDATE identity SET user_id=$1", signed.UserID)
	digest, version := rotated.Digest(phone1)
	id, _ := ids.New(ids.Identity)
	exec("INSERT INTO identity(id,user_id,kind,subject_digest,digest_key_version) VALUES($1,$2,'PHONE',$3,$4)", id, signed.UserID, digest, version)
	if _, e := read(); e == nil || errors.Is(e, user.ErrNotFound) {
		t.Fatalf("duplicate: %v", e)
	}
	exec("DELETE FROM identity WHERE id=$1", id)
	exec("UPDATE identity SET delete_time=now() WHERE user_id=$1", signed.UserID)
	if _, e := read(); !errors.Is(e, user.ErrNotFound) {
		t.Fatalf("unbind: %v", e)
	}
	other := f.signIn(t, enum.IdentityEmail, email1, dev1)
	exec("UPDATE identity SET user_id=$1, delete_time=NULL WHERE kind='PHONE'", other.UserID)
	if g, e := read(); e != nil || g.UserID != other.UserID {
		t.Fatalf("rebind: %+v %v", g, e)
	}
	exec("UPDATE identity SET delete_time=now() WHERE kind='PHONE'")
	exec("UPDATE user_account SET state=$1,display_name=NULL,avatar_url=NULL WHERE id=$2", enum.UserDeleted, other.UserID)
	if _, e := read(); !errors.Is(e, user.ErrNotFound) {
		t.Fatalf("anonymized: %v", e)
	}
}

func TestResolveIdentityConcurrentSnapshot(t *testing.T) {
	f := newFixture(t)
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	ctx := context.Background()
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			state := enum.UserActive
			name := "active"
			if i%2 == 0 {
				state = enum.UserFrozen
				name = "frozen"
			}
			if _, err := f.pool.Exec(ctx, "UPDATE user_account SET state=$1,display_name=$2 WHERE id=$3", state, name, signed.UserID); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 60; i++ {
		g, e := f.svc.ResolveIdentity(ctx, input)
		if e != nil {
			t.Fatal(e)
		}
		if (g.DisplayName == "active" && g.State != enum.UserActive) || (g.DisplayName == "frozen" && g.State != enum.UserFrozen) {
			t.Fatalf("mixed snapshot: %+v", g)
		}
	}
	wg.Wait()
}

func TestResolveIdentityUnavailableAndDeadline(t *testing.T) {
	f := newFixture(t)
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.svc.ResolveIdentity(ctx, input); !errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("cancel: %v", err)
	}
	closed, err := pgxpool.NewWithConfig(context.Background(), f.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Repo = user.NewRepo(closed)
	f.svc, err = user.NewService(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	start := time.Now()
	if _, err := f.svc.ResolveIdentity(context.Background(), input); !errors.Is(err, user.ErrUnavailable) {
		t.Fatalf("closed: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("deadline exceeded")
	}
}

func TestResolveIdentityProfileIsolationAndTimeout(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t)
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	ctx := context.Background()
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	if _, e := other.svc.ResolveIdentity(ctx, input); !errors.Is(e, user.ErrNotFound) {
		t.Fatalf("instance isolation: %v", e)
	}
	if _, e := f.pool.Exec(ctx, "UPDATE user_account SET state=$1,display_name='retained',avatar_url='https://example.invalid/avatar' WHERE id=$2", enum.UserPendingDeletion, signed.UserID); e != nil {
		t.Fatal(e)
	}
	got, e := f.svc.ResolveIdentity(ctx, input)
	if e != nil || got.DisplayName != "retained" || got.AvatarURL == "" {
		t.Fatalf("retained: %+v %v", got, e)
	}
	public, e := f.svc.BatchPublicProfiles(ctx, []string{signed.UserID})
	if e != nil || public[signed.UserID].DisplayName != "" || public[signed.UserID].AvatarURL != "" {
		t.Fatalf("public privacy: %+v %v", public, e)
	}
	tx, e := f.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "LOCK TABLE identity IN ACCESS EXCLUSIVE MODE"); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	_, e = f.svc.ResolveIdentity(ctx, input)
	elapsed := time.Since(start)
	if !errors.Is(e, user.ErrUnavailable) || elapsed < 2500*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("query deadline: %v %v", elapsed, e)
	}
}

type resolveFailingInserter struct{}

func (resolveFailingInserter) InsertBatch(context.Context, []audit.Event) (audit.BatchResult, error) {
	return audit.BatchResult{}, errors.New("synthetic storage failure")
}

func TestResolveIdentityAuditQueueAndWriteFailure(t *testing.T) {
	f := newFixture(t)
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	recorder := audit.NewAsync(resolveFailingInserter{}, audit.AsyncOptions{QueueSize: 1, BatchSize: 1, FlushInterval: time.Millisecond, Logger: f.deps.Logger})
	f.deps.Audit = recorder
	var e error
	f.svc, e = user.NewService(f.deps)
	if e != nil {
		t.Fatal(e)
	}
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	for i := 0; i < 3; i++ {
		g, e := f.svc.ResolveIdentity(context.Background(), input)
		if e != nil || g.UserID != signed.UserID {
			t.Fatalf("queue changed result: %+v %v", g, e)
		}
	}
	if recorder.Stats().Dropped == 0 {
		t.Fatal("queue not saturated")
	}
	recorder.Start(context.Background())
	defer recorder.Close()
	deadline := time.Now().Add(time.Second)
	for recorder.Stats().Failed == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if recorder.Stats().Failed == 0 {
		t.Fatal("write failure not observed")
	}
	if _, e := f.svc.ResolveIdentity(context.Background(), input); e != nil {
		t.Fatal(e)
	}
}

func TestResolveIdentityConcurrentRebindAndAnonymization(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	second := f.signIn(t, enum.IdentityEmail, email1, dev1)
	for id, name := range map[string]string{first.UserID: "first", second.UserID: "second"} {
		if _, e := f.pool.Exec(ctx, "UPDATE user_account SET display_name=$1 WHERE id=$2", name, id); e != nil {
			t.Fatal(e)
		}
	}
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			tx, e := f.pool.Begin(ctx)
			if e != nil {
				t.Error(e)
				return
			}
			id := first.UserID
			if i%2 == 0 {
				id = second.UserID
			}
			_, e = tx.Exec(ctx, "UPDATE identity SET user_id=$1,delete_time=NULL WHERE kind='PHONE'", id)
			if e == nil {
				_, e = tx.Exec(ctx, "UPDATE user_account SET state=$1 WHERE id=$2", enum.UserActive, id)
			}
			if e == nil {
				e = tx.Commit(ctx)
			} else {
				tx.Rollback(ctx)
			}
			if e != nil {
				t.Error(e)
				return
			}
		}
	}()
	for i := 0; i < 60; i++ {
		g, e := f.svc.ResolveIdentity(ctx, input)
		if e != nil {
			t.Fatal(e)
		}
		if (g.UserID == first.UserID && g.DisplayName != "first") || (g.UserID == second.UserID && g.DisplayName != "second") {
			t.Fatalf("mixed rebind: %+v", g)
		}
	}
	wg.Wait()
	tx, e := f.pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "UPDATE identity SET delete_time=now() WHERE kind='PHONE'"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "UPDATE user_account SET state=$1,display_name=NULL,avatar_url=NULL WHERE id IN ($2,$3)", enum.UserDeleted, first.UserID, second.UserID); e != nil {
		t.Fatal(e)
	}
	// 未提交匿名化不应拼接新资料；提交后立即不可再读到旧关联。
	if g, e := f.svc.ResolveIdentity(ctx, input); e != nil || g.DisplayName == "" {
		t.Fatalf("uncommitted anonymization leaked: %+v %v", g, e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.ResolveIdentity(ctx, input); !errors.Is(e, user.ErrNotFound) {
		t.Fatalf("committed anonymization: %v", e)
	}
}

func TestResolveIdentityDomainLifecycleConcurrency(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	signed := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, signed)
	if e := f.sendBindCode(ctx, p, enum.IdentityEmail, email1, meta1); e != nil {
		t.Fatal(e)
	}
	if _, _, e := f.svc.BindWithCode(ctx, p, f.credential(enum.PurposeBind, enum.IdentityEmail, email1, f.sent.code(email1)), meta1); e != nil {
		t.Fatal(e)
	}
	identities, e := f.svc.ListIdentities(ctx, signed.UserID)
	if e != nil {
		t.Fatal(e)
	}
	phoneID := ""
	for _, i := range identities {
		if i.Kind == enum.IdentityPhone {
			phoneID = i.ID
		}
	}
	input := user.ResolveIdentityInput{Kind: enum.IdentityPhone, Subject: phone1, Actor: audit.Actor{Type: audit.ActorService, Namespace: "services", ID: "business"}}
	done := make(chan error, 1)
	go func() { done <- f.svc.UnbindIdentity(ctx, p, phoneID, meta1) }()
	for i := 0; i < 20; i++ {
		g, e := f.svc.ResolveIdentity(ctx, input)
		if e != nil && !errors.Is(e, user.ErrNotFound) {
			t.Fatal(e)
		}
		if e == nil && g.UserID != signed.UserID {
			t.Fatal("wrong owner")
		}
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.ResolveIdentity(ctx, input); !errors.Is(e, user.ErrNotFound) {
		t.Fatalf("unbind committed: %v", e)
	}
	input.Kind = enum.IdentityEmail
	input.Subject = email1
	if _, e = f.svc.AdminDeleteUser(ctx, user.Admin{}, signed.UserID, meta1); e != nil {
		t.Fatal(e)
	}
	if g, e := f.svc.ResolveIdentity(ctx, input); e != nil || g.State != enum.UserPendingDeletion {
		t.Fatalf("deleted: %+v %v", g, e)
	}
	if _, e = f.svc.AdminUndeleteUser(ctx, user.Admin{}, signed.UserID, meta1); e != nil {
		t.Fatal(e)
	}
	if g, e := f.svc.ResolveIdentity(ctx, input); e != nil || g.State != enum.UserActive {
		t.Fatalf("restored: %+v %v", g, e)
	}
	if _, e = f.svc.AdminDeleteUser(ctx, user.Admin{}, signed.UserID, meta1); e != nil {
		t.Fatal(e)
	}
	f.advance(361 * time.Hour)
	go func() { _, e := f.svc.PurgeDueUsers(ctx); done <- e }()
	for i := 0; i < 20; i++ {
		g, e := f.svc.ResolveIdentity(ctx, input)
		if e != nil && !errors.Is(e, user.ErrNotFound) {
			t.Fatal(e)
		}
		if e == nil && (g.UserID != signed.UserID || g.State != enum.UserPendingDeletion) {
			t.Fatalf("mixed purge snapshot: %+v", g)
		}
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.ResolveIdentity(ctx, input); !errors.Is(e, user.ErrNotFound) {
		t.Fatalf("purge committed: %v", e)
	}
}
