package enduser

import (
	"fmt"
	"github.com/bbxx111/accountkit/user/sender"
	"net/http/httptest"
	"testing"
)

func TestDisabledChannelResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	testHandler(t).writeServiceError(rec, httptest.NewRequest("POST", "/x", nil), fmt.Errorf("wrap: %w", sender.ErrDisabled))
	reason, _ := aipErrorInternal(t, rec)
	if rec.Code != 400 || reason != "FAILED_PRECONDITION/CHANNEL_NOT_ENABLED" {
		t.Fatalf("got %d %s", rec.Code, reason)
	}
}
