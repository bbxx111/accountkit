package user_test

import (
	"context"
	"errors"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"testing"
)

func TestReplacementUnverifiedPrincipalDoesNotBecomeAuditActor(t *testing.T) {
	f, p, iid, _ := replacementSetup(t, enum.IdentityPhone, phone1)
	plain := replacementCode(t, f, p, enum.IdentityPhone, phone2)
	cred := f.credential(enum.PurposeBind, enum.IdentityPhone, phone2, plain)
	p.SessionID = "s_0000000000000"
	_, err := f.svc.ReplaceIdentity(context.Background(), p, iid, cred, meta1)
	if !errors.Is(err, user.ErrInvalidToken) {
		t.Fatalf("expected invalid session: %v", err)
	}
	found := false
	for _, e := range f.audit.RawEvents() {
		if e.Action == enum.EventIdentityReplaceRejected.String() {
			found = true
			if e.Actor.Type != audit.ActorUnknown || e.Actor.ID != "" {
				t.Fatalf("unverified Principal elevated to actor: %+v", e.Actor)
			}
		}
	}
	if !found {
		t.Fatal("rejection audit missing")
	}
}
