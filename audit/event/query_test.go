package event_test

import (
	"github.com/bbxx111/accountkit/audit/event"
	"slices"
	"strings"
	"testing"
)

func TestQueryNormalizesActionSetWithoutMutation(t *testing.T) {
	q := event.Query{Actions: []string{"SIGN_IN", "ORDER_APPROVED", "SIGN_IN"}}
	got, err := q.NormalizedActions()
	if err != nil || !slices.Equal(got, []string{"ORDER_APPROVED", "SIGN_IN"}) {
		t.Fatalf("normalized: %v %v", got, err)
	}
	if !slices.Equal(q.Actions, []string{"SIGN_IN", "ORDER_APPROVED", "SIGN_IN"}) {
		t.Fatalf("caller changed: %v", q.Actions)
	}
	got[0] = "CHANGED"
	if q.Actions[0] != "SIGN_IN" {
		t.Fatal("aliased result")
	}
	for _, actions := range [][]string{nil, {}, {""}, {"sign_in"}, {" SIGN_IN"}, {"SIGN_IN "}, {"ACTION-INVALID"}, {strings.Repeat("A", 129)}, {"动作"}} {
		n, err := (event.Query{Actions: actions}).NormalizedActions()
		if len(actions) == 0 {
			if err != nil || len(n) != 0 {
				t.Fatalf("empty: %v %v", n, err)
			}
		} else if err == nil {
			t.Fatalf("invalid accepted: %v", actions)
		}
	}
}
