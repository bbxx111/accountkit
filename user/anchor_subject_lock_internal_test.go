package user

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/pii"
)

// LockAnchorSubjects acquires every key in one statement. Ascending acquisition
// relies on the planner evaluating the volatile lock call above the Sort; pin
// that plan shape so a planner change cannot silently reorder lock acquisition.
func TestLockAnchorSubjectsLocksAfterSort(t *testing.T) {
	pool := profilesPool(t, &profilesTrace{})
	ctx := context.Background()
	src, err := os.ReadFile("query.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(src), "-- name: LockAnchorSubjects :exec\n")
	if !ok {
		t.Fatal("LockAnchorSubjects query not found")
	}
	block, _, _ = strings.Cut(block, "-- name:")
	var body []string
	for line := range strings.SplitSeq(block, "\n") {
		if !strings.HasPrefix(line, "--") {
			body = append(body, line)
		}
	}
	query := strings.NewReplacer("@kinds", "$1", "@digests", "$2").Replace(strings.Join(body, "\n"))
	var plan []struct {
		Plan explainNode `json:"Plan"`
	}
	var raw []byte
	if err := pool.QueryRow(ctx, "EXPLAIN (VERBOSE, COSTS OFF, FORMAT JSON) "+query,
		[]string{"PHONE", "PHONE", "EMAIL", "PHONE"}, []string{"d", "b", "a", "c"}).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &plan); err != nil || len(plan) != 1 {
		t.Fatalf("decode plan: %v", err)
	}
	locker := plan[0].Plan.find(func(n explainNode) bool { return n.computesLock() })
	if locker == nil {
		t.Fatalf("no plan node evaluates the lock: %s", raw)
	}
	if locker.find(func(n explainNode) bool { return n.NodeType == "Sort" }) == nil {
		t.Fatalf("lock is not evaluated above a Sort: %s", raw)
	}
	for _, child := range locker.Plans {
		if child.find(func(n explainNode) bool { return n.computesLock() }) != nil {
			t.Fatalf("lock evaluated below the ordering node: %s", raw)
		}
	}
}

type explainNode struct {
	NodeType string        `json:"Node Type"`
	Output   []string      `json:"Output"`
	Plans    []explainNode `json:"Plans"`
}

func (n explainNode) computesLock() bool {
	for _, out := range n.Output {
		if strings.Contains(out, "pg_advisory_xact_lock") {
			return true
		}
	}
	return false
}

// find returns the first node in pre-order, starting with n, that matches.
func (n explainNode) find(match func(explainNode) bool) *explainNode {
	if match(n) {
		return &n
	}
	for _, child := range n.Plans {
		if found := child.find(match); found != nil {
			return found
		}
	}
	return nil
}

// Processes in the mixed-active window of an HMAC rotation compute different
// active digests for one subject. Their identity writers must still exclude each
// other, while an unrelated subject proceeds independently.
func TestAnchorSubjectLockSpansDigestVersions(t *testing.T) {
	pool := profilesPool(t, &profilesTrace{})
	ctx := context.Background()
	keys := map[uint16][]byte{1: []byte("0123456789abcdef0123456789abcdef"), 2: []byte("fedcba9876543210fedcba9876543210")}
	service := func(active uint16) *Service {
		d, err := pii.NewDigester(keys, active)
		if err != nil {
			t.Fatal(err)
		}
		return &Service{d: Deps{Digester: d}}
	}
	oldActive, newActive := service(1), service(2)
	const target = "+8613800138000"
	oldDigest, _ := oldActive.d.Digester.Digest(target)
	newDigest, _ := newActive.d.Digester.Digest(target)
	if oldDigest == newDigest {
		t.Fatal("fixture must produce different active digests")
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if err := oldActive.lockAnchorSubjects(ctx, db.New(holder), anchorSubject{enum.IdentityPhone, target}); err != nil {
		t.Fatal(err)
	}
	attempt := func(svc *Service, subject anchorSubject, wait time.Duration) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		return svc.lockAnchorSubjects(waitCtx, db.New(tx), subject)
	}
	if err := attempt(newActive, anchorSubject{enum.IdentityPhone, target}, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("other active version acquired the same subject: %v", err)
	}
	if err := attempt(newActive, anchorSubject{enum.IdentityPhone, "+8613900139000"}, 5*time.Second); err != nil {
		t.Fatal("unrelated subject blocked:", err)
	}
	if err := attempt(newActive, anchorSubject{enum.IdentityEmail, target}, 5*time.Second); err != nil {
		t.Fatal("other identity kind blocked:", err)
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := attempt(newActive, anchorSubject{enum.IdentityPhone, target}, 5*time.Second); err != nil {
		t.Fatal("lock not released at commit:", err)
	}
}
