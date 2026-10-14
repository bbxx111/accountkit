package enduser_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

const replacementIdentityID = "i_0k3f9c2m1xq7z"
const replacementPath = "/users/me/identities/" + replacementIdentityID + ":replace"
const replacementBody = `{"email":{"code_id":"0123456789abcdef0123456789abcdef","target":"new@example.test","code":"123456"}}`

// The old fake deliberately has no replacement method; embedding preserves its interface.
var _ enduser.Service = (*fakeService)(nil)
var _ enduser.IdentityReplacer = (*replacementService)(nil)
var _ enduser.IdentityReplacer = (*user.Service)(nil)

type replacementService struct {
	*fakeService
	replace func(context.Context, user.Principal, string, user.CodeCredential, user.Meta) (user.IdentityInfo, error)
}

func (f *replacementService) ReplaceIdentity(ctx context.Context, p user.Principal, id string, cred user.CodeCredential, meta user.Meta) (user.IdentityInfo, error) {

	return f.replace(ctx, p, id, cred, meta)
}

func replacementHandler(t *testing.T, service enduser.Service, mutate func(*enduser.Deps)) http.Handler {
	t.Helper()
	d := enduser.Deps{
		Users: service, ClientIP: func(*http.Request) string { return "203.0.113.9" },
		RequestID: func(*http.Request) string { return "req-test" }, Now: func() time.Time { return testNow },
		ReauthMaxAge: 5 * time.Minute, SensitiveOpVerification: true,
	}
	if mutate != nil {
		mutate(&d)
	}
	h, err := enduser.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return h.Router()
}

func replacementResult(kind enum.IdentityKind) user.IdentityInfo {
	return user.IdentityInfo{ID: "i_0k3f9c2m1xq71", Kind: kind, MaskedSubject: "n**@example.test", CreateTime: testNow}
}

func TestIdentityReplacementOptionalCapability(t *testing.T) {
	f := withAuth(&fakeService{listIdentities: func(context.Context, string) ([]user.IdentityInfo, error) {
		return []user.IdentityInfo{replacementResult(enum.IdentityEmail)}, nil
	}}, principal)
	h := replacementHandler(t, f, nil)
	rec := do(t, h, call{method: "POST", path: replacementPath, bearer: "good", body: replacementBody})
	status, _ := aipError(t, rec)
	if rec.Code != 503 || status != "UNAVAILABLE/IDENTITY_REPLACEMENT_NOT_CONFIGURED" {
		t.Fatalf("old service replacement: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, h, call{method: "GET", path: "/users/me/identities", bearer: "good"}); rec.Code != 200 {
		t.Fatalf("old service list: %d %s", rec.Code, rec.Body.String())
	}
}

func TestIdentityReplacementReturnsMaskedResource(t *testing.T) {
	for _, tc := range []struct {
		name, body, target string
		kind               enum.IdentityKind
	}{
		{"email", replacementBody, "new@example.test", enum.IdentityEmail},
		{"phone", `{"phone":{"code_id":"0123456789abcdef0123456789abcdef","target":"+8613812345678","code":"123456"}}`, "+8613812345678", enum.IdentityPhone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &replacementService{fakeService: withAuth(&fakeService{}, principal), replace: func(_ context.Context, p user.Principal, id string, cred user.CodeCredential, meta user.Meta) (user.IdentityInfo, error) {
				kind, target, plainCode := cred.Channel, cred.Target, cred.Code

				if p != principal || id != replacementIdentityID || kind != tc.kind || target != tc.target || plainCode != "123456" || cred.CodeID != challengeID || meta.IP != "203.0.113.9" || meta.RequestID != "req-test" {
					t.Fatalf("incorrect replacement inputs: principal=%+v id=%q kind=%v meta=%+v", p, id, kind, meta)
				}
				return replacementResult(kind), nil
			}}
			rec := do(t, replacementHandler(t, f, nil), call{method: "POST", path: replacementPath, bearer: "good", body: tc.body})
			var out map[string]any
			decode(t, rec, &out)
			if rec.Code != 200 || len(out) != 4 || out["name"] != "users/u_0k3f9c2m1xq7z/identities/i_0k3f9c2m1xq71" || out["kind"] != tc.kind.String() || out["masked_subject"] != "n**@example.test" || out["create_time"] != "2026-09-10T12:00:00Z" {
				t.Fatalf("replacement response: %d %s", rec.Code, rec.Body.String())
			}
			for _, secret := range []string{tc.target, "123456", "access_token", "refresh_token", "subject_digest", "subject_ciphertext"} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatalf("response disclosed %q", secret)
				}
			}
		})
	}
}

func TestIdentityReplacementAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, bearer string
		p            user.Principal
		disabled     bool
		want         int
		reason       string
	}{
		{"missing bearer", "", principal, false, 401, "UNAUTHENTICATED/TOKEN_MISSING"},
		{"invalid bearer", "bad", principal, false, 401, "UNAUTHENTICATED/TOKEN_INVALID"},
		{"bind scope", "good", user.Principal{Scope: user.ScopeBind, AuthTime: testNow}, false, 403, "PERMISSION_DENIED/INSUFFICIENT_SCOPE"},
		{"undelete scope", "good", user.Principal{Scope: user.ScopeUndelete, AuthTime: testNow}, false, 403, "PERMISSION_DENIED/INSUFFICIENT_SCOPE"},
		{"stale", "good", user.Principal{Scope: user.ScopeUser, AuthTime: testNow.Add(-5*time.Minute - time.Second)}, false, 400, "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED"},
		{"missing auth time", "good", user.Principal{Scope: user.ScopeUser}, false, 400, "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED"},
		{"disabled still authenticates", "", principal, true, 401, "UNAUTHENTICATED/TOKEN_MISSING"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &replacementService{fakeService: withAuth(&fakeService{}, tc.p), replace: func(context.Context, user.Principal, string, user.CodeCredential, user.Meta) (user.IdentityInfo, error) {
				t.Fatal("rejected request reached replacement service")
				return user.IdentityInfo{}, nil
			}}
			rec := do(t, replacementHandler(t, f, func(d *enduser.Deps) { d.SensitiveOpVerification = !tc.disabled }), call{method: "POST", path: replacementPath, bearer: tc.bearer, body: "invalid JSON"})
			status, _ := aipError(t, rec)
			if rec.Code != tc.want || status != tc.reason {
				t.Fatalf("authentication: %d %s", rec.Code, rec.Body.String())
			}
			if rec.Code == 401 && rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 missing Bearer challenge")
			}
		})
	}
	for _, tc := range []struct {
		name     string
		age      time.Duration
		disabled bool
	}{{"boundary", 5 * time.Minute, false}, {"disabled stale", time.Hour, true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := principal
			p.AuthTime = testNow.Add(-tc.age)
			f := &replacementService{fakeService: withAuth(&fakeService{}, p), replace: func(context.Context, user.Principal, string, user.CodeCredential, user.Meta) (user.IdentityInfo, error) {
				return replacementResult(enum.IdentityEmail), nil
			}}
			rec := do(t, replacementHandler(t, f, func(d *enduser.Deps) { d.SensitiveOpVerification = !tc.disabled }), call{method: "POST", path: replacementPath, bearer: "good", body: replacementBody})
			if rec.Code != 200 {
				t.Fatalf("freshness allowed: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIdentityReplacementRejectsMalformedInputs(t *testing.T) {
	for _, tc := range []struct{ name, path, body, reason string }{
		{"bad id", "/users/me/identities/not-an-id:replace", replacementBody, "INVALID_ID"},
		{"wrong id kind", "/users/me/identities/u_0k3f9c2m1xq7z:replace", replacementBody, "INVALID_ID"},
		{"empty", replacementPath, "", "MALFORMED_BODY"},
		{"invalid json", replacementPath, "{", "MALFORMED_BODY"},
		{"null", replacementPath, "null", "MALFORMED_BODY"},
		{"unknown field", replacementPath, `{"email":{"code_id":"0123456789abcdef0123456789abcdef","target":"new@example.test","code":"123456","extra":true}}`, "MALFORMED_BODY"},
		{"extra object", replacementPath, replacementBody + ` {}`, "MALFORMED_BODY"},
		{"oversize", replacementPath, `{"email":{"code_id":"0123456789abcdef0123456789abcdef","target":"` + strings.Repeat("a", 64<<10) + `","code":"123456"}}`, "MALFORMED_BODY"},
		{"none", replacementPath, `{}`, "CREDENTIAL_ONEOF"},
		{"all null", replacementPath, `{"phone":null,"email":null}`, "CREDENTIAL_ONEOF"},
		{"two credentials", replacementPath, `{"email":{"code_id":"0123456789abcdef0123456789abcdef","target":"new@example.test","code":"123456"},"phone":{"code_id":"0123456789abcdef0123456789abcdef","target":"+8613812345678","code":"123456"}}`, "CREDENTIAL_ONEOF"},
		{"wechat", replacementPath, `{"wechat":{"app_id":"wx","code":"c"}}`, "CREDENTIAL_KIND_NOT_ALLOWED"},
		{"apple", replacementPath, `{"apple":{"id_token":"token","nonce":"n"}}`, "CREDENTIAL_KIND_NOT_ALLOWED"},
		{"missing code", replacementPath, `{"email":{"code_id":"0123456789abcdef0123456789abcdef","target":"new@example.test"}}`, "CREDENTIAL_INCOMPLETE"},
		{"missing target", replacementPath, `{"phone":{"code_id":"0123456789abcdef0123456789abcdef","code":"123456"}}`, "CREDENTIAL_INCOMPLETE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &replacementService{fakeService: withAuth(&fakeService{}, principal), replace: func(context.Context, user.Principal, string, user.CodeCredential, user.Meta) (user.IdentityInfo, error) {
				t.Fatal("malformed input reached service")
				return user.IdentityInfo{}, nil
			}}
			rec := do(t, replacementHandler(t, f, nil), call{method: "POST", path: tc.path, bearer: "good", body: tc.body})
			status, _ := aipError(t, rec)
			if rec.Code != 400 || status != "INVALID_ARGUMENT/"+tc.reason {
				t.Fatalf("malformed: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestIdentityReplacementErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		want   int
		status string
	}{
		{"unchanged", user.ErrIdentityUnchanged, 400, "INVALID_ARGUMENT/IDENTITY_UNCHANGED"},
		{"scope", user.ErrInsufficientScope, 403, "PERMISSION_DENIED/INSUFFICIENT_SCOPE"},
		{"reauth", user.ErrReauthenticationRequired, 400, "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED"},
		{"conflict", user.ErrIdentityConflict, 409, "ALREADY_EXISTS/IDENTITY_ALREADY_BOUND"},
		{"not found", user.ErrNotFound, 404, "NOT_FOUND/NOT_FOUND"},
		{"invalid token", user.ErrInvalidToken, 401, "UNAUTHENTICATED/TOKEN_INVALID"},
		{"frozen", user.ErrUserFrozen, 403, "PERMISSION_DENIED/USER_FROZEN"},
		{"pending deletion", user.ErrUserPendingDeletion, 403, "PERMISSION_DENIED/USER_PENDING_DELETION"},
		{"invalid state", user.ErrInvalidState, 400, "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE"},
		{"not anchor", user.ErrNotAnchor, 400, "FAILED_PRECONDITION/TARGET_NOT_ANCHOR"},
		{"kind mismatch", user.ErrInvalidArgument, 400, "INVALID_ARGUMENT/INVALID_ARGUMENT"},
		{"target", user.ErrInvalidTarget, 400, "INVALID_ARGUMENT/INVALID_TARGET"},
		{"limit", user.ErrIdentityKindLimit, 409, "ALREADY_EXISTS/IDENTITY_KIND_LIMIT"},
		{"code invalid", code.ErrInvalid, 400, "INVALID_ARGUMENT/CODE_INVALID"},
		{"code expired", code.ErrExpired, 400, "INVALID_ARGUMENT/CODE_EXPIRED"},
		{"code exhausted", code.ErrExhausted, 400, "INVALID_ARGUMENT/CODE_ATTEMPTS_EXHAUSTED"},
		{"rate limited", &code.RateLimitedError{Dimension: "TARGET_VERIFY_LIMIT", RetryAfter: 1500 * time.Millisecond}, 429, "RESOURCE_EXHAUSTED/TARGET_VERIFY_LIMIT"},
		{"unavailable", user.ErrUnavailable, 503, "UNAVAILABLE/DEPENDENCY_UNAVAILABLE"},
		{"internal", errors.New("private database detail"), 500, "INTERNAL/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &replacementService{fakeService: withAuth(&fakeService{}, principal), replace: func(context.Context, user.Principal, string, user.CodeCredential, user.Meta) (user.IdentityInfo, error) {
				return user.IdentityInfo{}, fmt.Errorf("replacement: %w", tc.err)
			}}
			rec := do(t, replacementHandler(t, f, nil), call{method: "POST", path: replacementPath, bearer: "good", body: replacementBody})
			status, body := aipError(t, rec)
			if rec.Code != tc.want || status != tc.status {
				t.Fatalf("error: %d %s", rec.Code, rec.Body.String())
			}
			if tc.err == user.ErrIdentityConflict && strings.Contains(body["message"].(string), "another account") {
				t.Fatal("conflict incorrectly discloses another account")
			}
			if tc.want == 401 && rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("invalid session missing Bearer challenge")
			}
			if tc.want == 500 && strings.Contains(rec.Body.String(), "private database detail") {
				t.Fatal("internal error exposed")
			}
			if tc.want == 429 && rec.Header().Get("Retry-After") != "2" {
				t.Fatalf("rate limit retry after: %s", rec.Header().Get("Retry-After"))
			}
			if tc.want == 503 && rec.Header().Get("Retry-After") != "1" {
				t.Fatalf("unavailable retry after: %s", rec.Header().Get("Retry-After"))
			}
		})
	}
}

func TestIdentityReplacementPreservesIdentityRoutes(t *testing.T) {
	f := &replacementService{fakeService: withAuth(&fakeService{
		unbindIdentity: func(_ context.Context, _ user.Principal, id string, _ user.Meta) error {
			if id != replacementIdentityID {
				t.Fatalf("DELETE id: %q", id)
			}
			return nil
		},
		bindWithCode: func(context.Context, user.Principal, user.CodeCredential, user.Meta) (user.IdentityInfo, bool, error) {
			return user.IdentityInfo{}, false, user.ErrIdentityConflict
		},
	}, principal), replace: func(context.Context, user.Principal, string, user.CodeCredential, user.Meta) (user.IdentityInfo, error) {
		return replacementResult(enum.IdentityEmail), nil
	}}
	h := replacementHandler(t, f, nil)
	if rec := do(t, h, call{method: "DELETE", path: "/users/me/identities/" + replacementIdentityID, bearer: "good"}); rec.Code != 204 {
		t.Fatalf("original DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, call{method: "POST", path: replacementPath, bearer: "good", body: replacementBody}); rec.Code != 200 {
		t.Fatalf("replace action: %d %s", rec.Code, rec.Body.String())
	}
	// DELETE retains its existing parameter validation: the action suffix is not an ID.
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"DELETE", replacementPath, 400}, {"POST", "/users/me/identities/" + replacementIdentityID, 405}} {
		rec := do(t, h, call{method: tc.method, path: tc.path, bearer: "good", body: replacementBody})
		if rec.Code != tc.want {
			t.Fatalf("wrong action method: %s %s => %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if tc.method == "DELETE" {
			if status, _ := aipError(t, rec); status != "INVALID_ARGUMENT/INVALID_ID" {
				t.Fatalf("DELETE action suffix: %s", rec.Body.String())
			}
		}
	}
	rec := do(t, h, call{method: "POST", path: "/users/me/identities", bearer: "good", body: replacementBody})
	_, body := aipError(t, rec)
	if rec.Code != 409 || !strings.Contains(body["message"].(string), "another account") {
		t.Fatalf("existing bind conflict changed: %d %s", rec.Code, rec.Body.String())
	}
}
