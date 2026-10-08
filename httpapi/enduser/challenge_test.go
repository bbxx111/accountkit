package enduser_test

import (
	"context"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"strings"
	"testing"
	"time"
)

const challengeID = "0123456789abcdef0123456789abcdef"

func TestCodeChallengeHTTPContract(t *testing.T) {
	challenge := user.CodeChallenge{CodeID: challengeID, ExpireTime: testNow.Add(5 * time.Minute)}
	f := withAuth(&fakeService{
		sendSignInCode: func(context.Context, enum.IdentityKind, string, user.Meta) (user.CodeChallenge, error) {
			return challenge, nil
		},
		sendBindCode: func(context.Context, user.Principal, enum.IdentityKind, string, user.Meta) (user.CodeChallenge, error) {
			return challenge, nil
		},
		sendReauthenticationCode: func(context.Context, user.Principal, enum.IdentityKind, string, user.Meta) (user.CodeChallenge, error) {
			return challenge, nil
		},
	}, principal)
	for _, path := range []string{"/users:sendSignInCode", "/users/me:sendBindCode", "/users/me:sendReauthenticationCode"} {
		for _, channel := range []string{"PHONE", "EMAIL"} {
			t.Run(path+channel, func(t *testing.T) {
				w := do(t, newHandler(t, f), call{method: "POST", path: path, bearer: "good", body: map[string]string{"channel": channel, "target": "person@example.test"}})
				var out struct {
					CodeID     string    `json:"code_id"`
					ExpireTime time.Time `json:"expire_time"`
				}
				decode(t, w, &out)
				if w.Code != 200 || out.CodeID != challengeID || !out.ExpireTime.Equal(challenge.ExpireTime) || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal(w.Code, w.Header(), w.Body.String())
				}
			})
		}
	}
	check := func(c user.CodeCredential) error {
		if c.CodeID != challengeID {
			t.Fatal("credential code_id was not forwarded")
		}
		switch c.Code {
		case "expired":
			return code.ErrExpired
		case "wrong":
			return code.ErrInvalid
		case "limit":
			return &code.RateLimitedError{Dimension: "TARGET_VERIFY_LIMIT", RetryAfter: 21 * time.Second}
		case "down":
			return user.ErrUnavailable
		}
		return nil
	}
	f.signInWithCode = func(_ context.Context, c user.CodeCredential, _ user.Device, _ user.Meta) (user.TokenResult, error) {
		return user.TokenResult{}, check(c)
	}
	f.bindWithCode = func(_ context.Context, _ user.Principal, c user.CodeCredential, _ user.Meta) (user.IdentityInfo, bool, error) {
		return user.IdentityInfo{}, false, check(c)
	}
	f.reauthenticate = func(_ context.Context, _ user.Principal, c user.CodeCredential, _ user.Meta) (user.TokenResult, error) {
		return user.TokenResult{}, check(c)
	}
	h := replacementHandler(t, &replacementService{fakeService: f, replace: func(_ context.Context, _ user.Principal, _ string, c user.CodeCredential, _ user.Meta) (user.IdentityInfo, error) {
		return user.IdentityInfo{}, check(c)
	}}, nil)
	for _, path := range []string{"/users:signInWithCode", "/users/me/identities", "/users/me:reauthenticate", replacementPath} {
		for _, channel := range []string{"phone", "email"} {
			for _, badID := range []string{"", "UPPER0123456789abcdef0123456789ab", "abcd"} {
				w := do(t, h, call{method: "POST", path: path, bearer: "good", headers: devHeaders(), body: map[string]any{channel: map[string]string{"target": "person@example.test", "code": "valid", "code_id": badID}}})
				if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_ARGUMENT") {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			for _, tc := range []struct {
				plain, reason string
				status        int
			}{{"expired", "CODE_EXPIRED", 400}, {"wrong", "CODE_INVALID", 400}, {"limit", "TARGET_VERIFY_LIMIT", 429}, {"down", "DEPENDENCY_UNAVAILABLE", 503}} {
				w := do(t, h, call{method: "POST", path: path, bearer: "good", headers: devHeaders(), body: map[string]any{channel: map[string]string{"target": "person@example.test", "code": tc.plain, "code_id": challengeID}}})
				if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.reason) || w.Header().Get("WWW-Authenticate") != "" {
					t.Fatal(w.Code, w.Header(), w.Body.String())
				}
				if tc.status == 429 && w.Header().Get("Retry-After") != "21" {
					t.Fatal(w.Header())
				}
			}
		}
	}
}
