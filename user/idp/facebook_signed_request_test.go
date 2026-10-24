package idp_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/user/idp"
)

func signRequest(secret, payload string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(payload))
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) + "." + p
}

func TestFacebookParseSignedRequest(t *testing.T) {
	fb := idp.NewFacebook(idp.FacebookOptions{AppID: fbApp, AppSecret: fbSecret})
	good := signRequest(fbSecret, `{"algorithm":"HMAC-SHA256","issued_at":1792756800,"user_id":"asid-1"}`)
	req, err := fb.ParseSignedRequest(good)
	if err != nil || req.UserID != "asid-1" || !req.IssueTime.Equal(time.Unix(1792756800, 0)) {
		t.Fatalf("good: %+v %v", req, err)
	}
	sig, payload, _ := strings.Cut(good, ".")
	for name, raw := range map[string]string{
		"wrong secret": signRequest("other", `{"algorithm":"HMAC-SHA256","issued_at":1,"user_id":"asid-1"}`),
		"wrong alg":    signRequest(fbSecret, `{"algorithm":"HMAC-SHA1","issued_at":1,"user_id":"asid-1"}`),
		"no user":      signRequest(fbSecret, `{"algorithm":"HMAC-SHA256","issued_at":1}`),
		"no issued_at": signRequest(fbSecret, `{"algorithm":"HMAC-SHA256","user_id":"asid-1"}`),
		"bad json":     signRequest(fbSecret, `{`),
		"one part":     payload,
		"three parts":  good + ".x",
		"padded":       sig + "=." + payload,
		"empty":        "",
	} {
		_, err := fb.ParseSignedRequest(raw)
		if !errors.Is(err, idp.ErrInvalidSignedRequest) || strings.Contains(err.Error(), fbSecret) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
