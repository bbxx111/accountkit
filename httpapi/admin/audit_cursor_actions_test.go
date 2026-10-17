package admin

import (
	"encoding/base64"
	"encoding/json"
	"github.com/bbxx111/accountkit/audit"
	"github.com/google/uuid"
	"slices"
	"testing"
	"time"
)

func TestAuditCursorCanonicalActionSet(t *testing.T) {
	q := audit.Query{Source: "test/source", Resource: audit.Resource{Type: "USER", Namespace: "test/ns", ID: "u_0000000000001"}, Actions: []string{"SIGN_IN", "CODE_SENT", "SIGN_IN"}}
	last := audit.EventCursor{Time: time.Now().UTC().Truncate(time.Microsecond), ID: uuid.Must(uuid.NewV7())}
	token := encodeAuditCursor(q, last)
	var wire struct {
		Version int      `json:"v"`
		Actions []string `json:"actions"`
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Version != 2 || !slices.Equal(wire.Actions, []string{"CODE_SENT", "SIGN_IN"}) {
		t.Fatalf("unbound/noncanonical cursor: %+v", wire)
	}
	q.Actions = []string{"CODE_SENT", "SIGN_IN"}
	after, e := decodeAuditCursor(token, q)
	if e != nil || after == nil || *after != last {
		t.Fatalf("equivalent action set: %+v %v", after, e)
	}
	q.Actions = []string{"SIGN_IN"}
	if _, e = decodeAuditCursor(token, q); e == nil {
		t.Fatal("cross action cursor accepted")
	}
}
