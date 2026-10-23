package accountkit_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

const facebookTestSecret = "fb-test-secret"

// signRequest 按 Facebook signed_request 格式（HMAC-SHA256，base64url 无填充）构造请求。
func signRequest(secret, payload string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(payload))
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) + "." + p
}

func TestHandleFacebookDataDeletion(t *testing.T) {
	ctx := context.Background()
	c := minimal()
	a, err := accountkit.New(c, baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleFacebookDataDeletion(ctx, "x.y", user.Meta{}); !errors.Is(err, accountkit.ErrFacebookNotConfigured) {
		t.Fatalf("not configured: %v", err)
	}

	c.FacebookAppID, c.FacebookAppSecret = "123", facebookTestSecret
	a, err = accountkit.New(c, baseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleFacebookDataDeletion(ctx, signRequest("wrong", `{"algorithm":"HMAC-SHA256","issued_at":1,"user_id":"u"}`), user.Meta{}); !errors.Is(err, idp.ErrInvalidSignedRequest) {
		t.Fatalf("bad signature: %v", err)
	}
}

func TestHandleFacebookDataDeletionUnknownUserAgainstRealDB(t *testing.T) {
	ctx := context.Background()
	cfg := minimal()
	cfg.FacebookAppID, cfg.FacebookAppSecret = "123", facebookTestSecret
	a, _, _ := integrationInstance(t, cfg, testRedis(t))
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	payload := `{"algorithm":"HMAC-SHA256","issued_at":1792756800,"user_id":"fb-unknown-subject"}`
	res, err := a.HandleFacebookDataDeletion(ctx, signRequest(facebookTestSecret, payload), user.Meta{})
	if err != nil {
		t.Fatalf("unknown user: %v", err)
	}
	if res.Status != user.ProviderDeletionDeleted || res.ConfirmationCode == "" {
		t.Fatalf("unknown user result: %+v", res)
	}
}
