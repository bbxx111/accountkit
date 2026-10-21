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

type profileAttributesRecorder struct {
	f         *fixture
	events    []audit.Event
	committed bool
}

func (r *profileAttributesRecorder) Record(ctx context.Context, e audit.Event) {
	if e.Action != "USER_PROFILE_ATTRIBUTES_UPDATED" {
		return
	}
	var committed bool
	err := r.f.pool.QueryRow(ctx, `SELECT profile_attributes_schema_version IS NOT NULL FROM user_account WHERE id=$1`, e.Targets[0].ID).Scan(&committed)
	r.committed = err == nil && committed
	r.events = append(r.events, e)
}
func TestProfileAttributesAuditPrivacy(t *testing.T) {
	f, id := profileAttributesFixture(t)
	r := &profileAttributesRecorder{f: f}
	newServiceWith(t, f, func(d *user.Deps) {
		d.Audit = r
		d.AuditSource = "synthetic/source"
		d.AuditNamespace = "synthetic/resources"
	})
	_, err := f.svc.PatchProfileAttributes(context.Background(), id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"synthetic-private-value"}`)}, user.Meta{RequestID: "req-ext", IP: "203.0.113.43", ClientInfo: &user.ClientInfo{}})
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
	_, err = f.svc.PatchProfileAttributes(context.Background(), id, user.ProfileAttributesPatch{Document: []byte(`{}`)}, meta1)
	if err != nil || len(r.events) != 1 {
		t.Fatal("noop event", err)
	}
	// PostgreSQL check violation Detail includes NEW row; exported errors must not retain it.
	mustExec(t, f, `ALTER TABLE user_account ADD CONSTRAINT synthetic_reject_extensions CHECK (profile_attributes->>'theme' <> 'second-private-value')`)
	_, err = f.svc.PatchProfileAttributes(context.Background(), id, user.ProfileAttributesPatch{Document: []byte(`{"theme":"second-private-value"}`)}, meta1)
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), "private") || len(r.events) != 1 {
		t.Fatalf("unsafe storage error or success audit: %v", err)
	}
}
