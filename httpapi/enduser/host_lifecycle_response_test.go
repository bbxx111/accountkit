package enduser

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/user"
)

func TestDeletionBlockedSafeResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		code    int
		status  string
		reason  string
		message string
	}{
		{"business", fmt.Errorf("private owner id: %w", user.ErrDeletionBlocked), 400, "FAILED_PRECONDITION", "DELETION_BLOCKED", "account deletion is blocked"},
		{"infrastructure", errors.New("private host database details"), 500, "INTERNAL", "", "internal error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			testHandler(t).writeServiceError(rec, httptest.NewRequest("DELETE", "/users/me", nil), tc.err)
			got, body := aipErrorInternal(t, rec)
			if rec.Code != tc.code || got != tc.status+"/"+tc.reason || body["message"] != tc.message {
				t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "private") {
				t.Fatal("host error leaked into response")
			}
		})
	}
}
