package admin_test

import (
	"testing"
	"time"

	"github.com/bbxx111/accountkit/httpapi/admin"
)

func TestParsePageSize(t *testing.T) {
	for in, want := range map[string]int{"": 20, "0": 20, "1": 1, "37": 37, "100": 100, "101": 100, "99999": 100} {
		got, e := admin.ParsePageSizeForTest(in)
		if e != nil || got != want {
			t.Fatalf("%q: got %d err=%v want %d", in, got, e, want)
		}
	}
	for _, bad := range []string{"-1", "abc", "1.5", " 3"} {
		if _, e := admin.ParsePageSizeForTest(bad); e == nil || e.Reason != "INVALID_PAGE_SIZE" {
			t.Fatalf("%q must be rejected: %+v", bad, e)
		}
	}
}

func TestCursorRoundTripAndRejection(t *testing.T) {
	at := time.Date(2026, 9, 14, 8, 30, 0, 123456789, time.FixedZone("CST", 8*3600))
	tok := admin.EncodeCursorForTest(at, "u_0k3f9c2m1xq7z")
	gotT, gotID, ok, e := admin.DecodeCursorForTest(tok)
	if e != nil || !ok || gotID != "u_0k3f9c2m1xq7z" || !gotT.Equal(at) {
		t.Fatalf("round trip: %v %q %v %v", gotT, gotID, ok, e)
	}
	if _, _, ok, e := admin.DecodeCursorForTest(""); ok || e != nil {
		t.Fatalf("empty token must mean no cursor: %v %v", ok, e)
	}
	for _, bad := range []string{"not-base64!", "e30", "eyJ0IjoiYmFkIiwiaWQiOiJ4In0"} { // {} / {"t":"bad","id":"x"}
		if _, _, _, e := admin.DecodeCursorForTest(bad); e == nil || e.Reason != "INVALID_PAGE_TOKEN" {
			t.Fatalf("%q must be rejected: %+v", bad, e)
		}
	}
}
