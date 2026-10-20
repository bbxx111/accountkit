package user_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/user"
)

type extensionsRecorder struct {
	f         *fixture
	events    []audit.Event
	committed bool
}

func (r *extensionsRecorder) Record(ctx context.Context, e audit.Event) {
	if e.Action != "USER_EXTENSIONS_UPDATED" {
		return
	}
	var rev int64
	err := r.f.pool.QueryRow(ctx, `SELECT extensions_revision FROM user_account WHERE id=$1`, e.Targets[0].ID).Scan(&rev)
	r.committed = err == nil && rev == 1
	r.events = append(r.events, e)
}
func TestUserExtensionsAuditPrivacy(t *testing.T) {
	f, id := extensionsFixture(t)
	r := &extensionsRecorder{f: f}
	newServiceWith(t, f, func(d *user.Deps) {
		d.Audit = r
		d.AuditSource = "synthetic/source"
		d.AuditNamespace = "synthetic/resources"
	})
	z := int64(0)
	v, err := f.svc.PatchExtensions(context.Background(), id, user.ExtensionsPatch{Document: []byte(`{"theme":"synthetic-private-value"}`), ExpectedRevision: &z}, user.Meta{RequestID: "req-ext", IP: "203.0.113.43", ClientInfo: &user.ClientInfo{}})
	if err != nil || !r.committed || len(r.events) != 1 {
		t.Fatalf("audit after commit: %v %v", err, r.events)
	}
	e := r.events[0]
	if e.Actor.Type != audit.ActorService || e.Actor.ID != "accountkit-host" || e.Actor.ID == id || e.Source != "synthetic/source" || e.Context.RequestID != "req-ext" || e.Context.ClientIP != "" || e.Targets[0].ID != id {
		t.Fatalf("event metadata: %+v", e)
	}
	b, _ := json.Marshal(e)
	for _, secret := range []string{"theme", "synthetic-private-value", "203.0.113.43", "properties"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("event leaked %q", secret)
		}
	}
	_, err = f.svc.PatchExtensions(context.Background(), id, user.ExtensionsPatch{Document: []byte(`{}`), ExpectedRevision: &v.Revision}, meta1)
	if err != nil || len(r.events) != 1 {
		t.Fatal("noop event", err)
	}
	// PostgreSQL check violation Detail includes NEW row; exported errors must not retain it.
	mustExec(t, f, `ALTER TABLE user_account ADD CONSTRAINT synthetic_reject_extensions CHECK (extensions_revision < 2)`)
	_, err = f.svc.PatchExtensions(context.Background(), id, user.ExtensionsPatch{Document: []byte(`{"theme":"second-private-value"}`), ExpectedRevision: &v.Revision}, meta1)
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), "private") || len(r.events) != 1 {
		t.Fatalf("unsafe storage error or success audit: %v", err)
	}
}
