package ids

import "testing"

func TestIdentityFlowID(t *testing.T) {
	id, err := New(IdentityFlow)
	if err != nil || !Valid(IdentityFlow, id) || Valid(User, id) || Pattern(IdentityFlow) != `^f_[0-9a-hjkmnp-tv-z]{13}$` {
		t.Fatalf("flow id %s %v", id, err)
	}
}
