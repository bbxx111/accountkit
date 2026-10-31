package ids

import "testing"

func TestSignInCompletionFlowID(t *testing.T) {
	id, err := New(SignInCompletionFlow)
	if err != nil || !Valid(SignInCompletionFlow, id) || Valid(User, id) || Pattern(SignInCompletionFlow) != `^f_[0-9a-hjkmnp-tv-z]{13}$` {
		t.Fatalf("flow id %s %v", id, err)
	}
}
