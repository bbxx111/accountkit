package enum

import "testing"

func TestFlowStateAndCompletionPurpose(t *testing.T) {
	for i, s := range []FlowState{FlowPending, FlowVerified, FlowCompleted, FlowExpired, FlowRejected} {
		if int(s) != i+1 || !s.Valid() {
			t.Fatal(s)
		}
		if got, err := ParseFlowState(s.String()); err != nil || got != s {
			t.Fatalf("parse %v %v", got, err)
		}
	}
	if PurposeSignInComplete != 4 || !PurposeSignInComplete.Valid() || PurposeSignInComplete.String() != "SIGN_IN_COMPLETE" || PurposeSignInComplete.Key() != "signin_complete" {
		t.Fatal("purpose compatibility")
	}
	if FlowStateUnspecified.Valid() {
		t.Fatal("zero state")
	}
}
