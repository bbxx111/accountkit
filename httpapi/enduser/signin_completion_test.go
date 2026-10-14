package enduser_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

type completionService struct {
	*fakeService
	send     func(context.Context, string, enum.IdentityKind, string, user.Device, user.Meta) (user.CodeChallenge, error)
	complete func(context.Context, string, user.CodeCredential, user.Device, user.Meta) (user.SignInResult, error)
}

func (f *completionService) SendSignInCompletionCode(ctx context.Context, token string, ch enum.IdentityKind, target string, dev user.Device, meta user.Meta) (user.CodeChallenge, error) {
	return f.send(ctx, token, ch, target, dev, meta)
}

func (f *completionService) CompleteSignIn(ctx context.Context, token string, cred user.CodeCredential, dev user.Device, meta user.Meta) (user.SignInResult, error) {
	return f.complete(ctx, token, cred, dev, meta)
}

func completionHandler(t *testing.T, f *completionService) http.Handler {
	t.Helper()
	h, err := enduser.New(enduser.Deps{Users: f, ClientIP: func(*http.Request) string { return "203.0.113.9" }, RequestID: func(*http.Request) string { return "req-test" }, ReauthMaxAge: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return h.Router()
}

const completionCodeBody = `{"sign_in_completion_token":"opaque","channel":"EMAIL","target":"a@example.test"}`
const completionBody = `{"sign_in_completion_token":"opaque","email":{"target":"a@example.test","code_id":"0123456789abcdef0123456789abcdef","code":"123456"}}`

func TestSignInCompletionEndpoints(t *testing.T) {
	f := &completionService{fakeService: &fakeService{}}
	f.send = func(_ context.Context, token string, ch enum.IdentityKind, target string, dev user.Device, meta user.Meta) (user.CodeChallenge, error) {
		if token != "opaque" || ch != enum.IdentityEmail || target != "a@example.test" || dev != (user.Device{}) || meta != (user.Meta{IP: "203.0.113.9", RequestID: "req-test"}) {
			t.Fatalf("send args: %q %v %q %+v %+v", token, ch, target, dev, meta)
		}
		return user.CodeChallenge{CodeID: "challenge", ExpireTime: testNow}, nil
	}
	f.complete = func(_ context.Context, token string, cred user.CodeCredential, dev user.Device, meta user.Meta) (user.SignInResult, error) {
		if token != "opaque" || cred != (user.CodeCredential{Channel: enum.IdentityEmail, Target: "a@example.test", CodeID: "0123456789abcdef0123456789abcdef", Code: "123456"}) || dev != (user.Device{ID: devHeaders()["X-Device-Id"], Name: "Pixel 9"}) || meta.RequestID != "req-test" || meta.IP != "203.0.113.9" {
			t.Fatalf("complete args: %q %+v %+v %+v", token, cred, dev, meta)
		}
		return user.SignInResult{Tokens: &user.TokenResult{AccessToken: "at", RefreshToken: "rt", Scope: "user", UserID: "u_1", IsNewUser: false}}, nil
	}
	h := completionHandler(t, f)
	// These public endpoints authenticate through the body; a supplied Bearer must not replace it.
	r := do(t, h, call{method: "POST", path: "/accounts:sendSignInCompletionCode", body: completionCodeBody, bearer: "bad"})
	if r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(r.Body.String(), `"code_id":"challenge"`) {
		t.Fatalf("send: %d %s", r.Code, r.Body.String())
	}
	r = do(t, h, call{method: "POST", path: "/accounts:completeSignIn", body: completionBody, headers: devHeaders(), bearer: "bad"})
	var got map[string]any
	decode(t, r, &got)
	if r.Code != 200 || got["access_token"] != "at" || got["user_id"] != "u_1" || got["is_new_user"] != false || got["token_type"] != "Bearer" || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("complete: %d %v", r.Code, got)
	}
}

func TestSignInCompletionPending(t *testing.T) {
	f := &completionService{fakeService: &fakeService{}, complete: func(context.Context, string, user.CodeCredential, user.Device, user.Meta) (user.SignInResult, error) {
		return user.SignInResult{Pending: &user.PendingSignIn{SignInCompletionToken: "pending", ExpireTime: testNow, EnabledIdentityKinds: []enum.IdentityKind{enum.IdentityEmail, enum.IdentityWeChat}}}, nil
	}}
	r := do(t, completionHandler(t, f), call{method: "POST", path: "/accounts:completeSignIn", body: completionBody})
	var got map[string]any
	decode(t, r, &got)
	want := map[string]any{"next_action": "COMPLETE_IDENTITY", "sign_in_completion_token": "pending", "expire_time": "2026-09-10T12:00:00Z", "enabled_identity_kinds": []any{"EMAIL", "WECHAT"}, "required_identity_kinds": []any{}}
	if r.Code != 200 || !reflect.DeepEqual(got, want) || r.Header().Get("Cache-Control") != "no-store" || r.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("pending: %d %v", r.Code, got)
	}
}

func TestSignInCompletionRejectsMalformedCredentials(t *testing.T) {
	h := completionHandler(t, &completionService{fakeService: &fakeService{}})
	for _, body := range []string{
		`{"sign_in_completion_token":"opaque"}`,
		`{"sign_in_completion_token":"opaque","phone":{},"email":{}}`,
		`{"sign_in_completion_token":"opaque","wechat":{"app_id":"wx","code":"secret"}}`,
		`{"sign_in_completion_token":"opaque","apple":{"id_token":"secret","nonce":"n"}}`,
		strings.Replace(completionBody, `"code":"123456"`, `"code":"123456","unknown":true`, 1),
		strings.Replace(completionBody, `"code_id":"0123456789abcdef0123456789abcdef",`, ``, 1),
	} {
		r := do(t, h, call{method: "POST", path: "/accounts:completeSignIn", body: body})
		if r.Code != 400 {
			t.Errorf("body %s: %d %s", body, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{"/accounts:sendSignInCompletionCode", "/accounts:completeSignIn"} {
		base := completionBody
		if strings.Contains(path, "send") {
			base = completionCodeBody
		}
		for _, alias := range []string{"continuation_token", "proof_token"} {
			r := do(t, h, call{method: "POST", path: path, body: strings.Replace(base, "sign_in_completion_token", alias, 1)})
			if s, _ := aipError(t, r); r.Code != 400 || s != "INVALID_ARGUMENT/MALFORMED_BODY" {
				t.Errorf("%s %s: %d %s", path, alias, r.Code, s)
			}
		}
		for _, extra := range []string{`"purpose":"SIGN_IN_COMPLETE"`, `"defer_registration":true`, `"device_id":"override"`} {
			r := do(t, h, call{method: "POST", path: path, body: strings.TrimSuffix(base, "}") + "," + extra + "}"})
			if s, _ := aipError(t, r); r.Code != 400 || s != "INVALID_ARGUMENT/MALFORMED_BODY" {
				t.Errorf("%s extra: %d %s", path, r.Code, s)
			}
		}
		body := strings.Replace(base, `"sign_in_completion_token":"opaque",`, "", 1)
		r := do(t, h, call{method: "POST", path: path, body: body, bearer: "good"})
		if s, _ := aipError(t, r); r.Code != 400 || s != "FAILED_PRECONDITION/SIGN_IN_COMPLETION_INVALID" {
			t.Errorf("missing token: %d %s", r.Code, s)
		}
	}
	for _, ch := range []string{"WECHAT", "APPLE", "UNSPECIFIED", "email"} {
		r := do(t, h, call{method: "POST", path: "/accounts:sendSignInCompletionCode", body: strings.Replace(completionCodeBody, "EMAIL", ch, 1)})
		if s, _ := aipError(t, r); r.Code != 400 || s != "INVALID_ARGUMENT/CHANNEL_INVALID" {
			t.Errorf("channel: %d %s", r.Code, s)
		}
	}
}

func TestSignInCompletionOptionalCapability(t *testing.T) {
	h := newHandler(t, &fakeService{})
	for path, body := range map[string]string{"/accounts:sendSignInCompletionCode": completionCodeBody, "/accounts:completeSignIn": completionBody} {
		r := do(t, h, call{method: "POST", path: path, body: body})
		if s, _ := aipError(t, r); r.Code != 503 || s != "UNAVAILABLE/SIGN_IN_COMPLETION_NOT_CONFIGURED" {
			t.Fatalf("%s: %d %s", path, r.Code, s)
		}
	}
}

func TestSignInCompletionErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   int
		status string
	}{
		{user.ErrSignInCompletionInvalid, 400, "FAILED_PRECONDITION/SIGN_IN_COMPLETION_INVALID"},
		{user.ErrSignInAlreadyCompleted, 400, "FAILED_PRECONDITION/SIGN_IN_ALREADY_COMPLETED"},
		{user.ErrIdentityKindNotEnabled, 400, "FAILED_PRECONDITION/IDENTITY_KIND_NOT_ENABLED"},
		{code.ErrInvalid, 400, "INVALID_ARGUMENT/CODE_INVALID"},
		{user.ErrUnavailable, 503, "UNAVAILABLE/DEPENDENCY_UNAVAILABLE"},
		{errors.New("private-secret"), 500, "INTERNAL/"},
	} {
		f := &completionService{fakeService: &fakeService{}, send: func(context.Context, string, enum.IdentityKind, string, user.Device, user.Meta) (user.CodeChallenge, error) {
			return user.CodeChallenge{}, tc.err
		}, complete: func(context.Context, string, user.CodeCredential, user.Device, user.Meta) (user.SignInResult, error) {
			return user.SignInResult{}, tc.err
		}}
		for path, body := range map[string]string{"/accounts:sendSignInCompletionCode": completionCodeBody, "/accounts:completeSignIn": completionBody} {
			r := do(t, completionHandler(t, f), call{method: "POST", path: path, body: body})
			if s, _ := aipError(t, r); r.Code != tc.code || s != tc.status || strings.Contains(r.Body.String(), "private-secret") || r.Header().Get("WWW-Authenticate") != "" {
				t.Errorf("%s: %d %s", path, r.Code, r.Body.String())
			}
		}
	}
}

func TestOrdinarySignInCodeRejectsCompletionFields(t *testing.T) {
	h := newHandler(t, &fakeService{})
	for _, extra := range []string{`"purpose":"SIGN_IN_COMPLETE"`, `"sign_in_completion_token":"opaque"`} {
		r := do(t, h, call{method: "POST", path: "/accounts:sendSignInCode", body: `{"channel":"EMAIL","target":"a@example.test",` + extra + `}`})
		if s, _ := aipError(t, r); r.Code != 400 || s != "INVALID_ARGUMENT/MALFORMED_BODY" {
			t.Fatalf("%d %s", r.Code, s)
		}
	}
}

func TestSignInCompletionRejectsInvalidResultUnion(t *testing.T) {
	for _, res := range []user.SignInResult{{}, {Tokens: &user.TokenResult{AccessToken: "secret"}, Pending: &user.PendingSignIn{SignInCompletionToken: "secret"}}} {
		f := &completionService{fakeService: &fakeService{}, complete: func(context.Context, string, user.CodeCredential, user.Device, user.Meta) (user.SignInResult, error) {
			return res, nil
		}}
		r := do(t, completionHandler(t, f), call{method: "POST", path: "/accounts:completeSignIn", body: completionBody})
		if s, _ := aipError(t, r); r.Code != 500 || s != "INTERNAL/" || strings.Contains(r.Body.String(), "secret") {
			t.Fatalf("union: %d %s", r.Code, r.Body.String())
		}
	}
}

func TestInitialSignInResultUnion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result user.SignInResult
		code   int
	}{
		{"pending", user.SignInResult{Pending: &user.PendingSignIn{SignInCompletionToken: "opaque", ExpireTime: testNow, EnabledIdentityKinds: []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}, RequiredIdentityKinds: []enum.IdentityKind{enum.IdentityEmail}}}, 200},
		{"absent", user.SignInResult{}, 500},
		{"both", user.SignInResult{Tokens: &user.TokenResult{AccessToken: "secret"}, Pending: &user.PendingSignIn{SignInCompletionToken: "secret"}}, 500},
	} {
		f := &fakeService{signInWithCode: func(context.Context, user.CodeCredential, user.Device, user.Meta) (user.SignInResult, error) {
			return tc.result, nil
		}, signInWithIdp: func(context.Context, user.IdpCredential, user.Device, user.Meta) (user.SignInResult, error) {
			return tc.result, nil
		}}
		for path, body := range map[string]string{"/accounts:signInWithCode": `{"phone":{"target":"+8613812341234","code_id":"0123456789abcdef0123456789abcdef","code":"123456"}}`, "/accounts:signInWithIdp": `{"wechat":{"app_id":"wx","code":"secret"}}`} {
			t.Run(tc.name+path, func(t *testing.T) {
				r := do(t, newHandler(t, f), call{method: "POST", path: path, body: body, headers: devHeaders()})
				if r.Code != tc.code {
					t.Fatalf("result: %d %s", r.Code, r.Body.String())
				}
				if tc.code == 500 {
					if s, _ := aipError(t, r); s != "INTERNAL/" || strings.Contains(r.Body.String(), "secret") {
						t.Fatalf("internal error: %s", r.Body.String())
					}
					return
				}
				var got map[string]any
				decode(t, r, &got)
				want := map[string]any{"next_action": "COMPLETE_IDENTITY", "sign_in_completion_token": "opaque", "expire_time": "2026-09-10T12:00:00Z", "enabled_identity_kinds": []any{"PHONE", "EMAIL"}, "required_identity_kinds": []any{"EMAIL"}}
				if !reflect.DeepEqual(got, want) || r.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("pending: %v", got)
				}
			})
		}
	}
}

func TestCompletionOneofDescribesOnlySupportedCredentials(t *testing.T) {
	h := completionHandler(t, &completionService{fakeService: &fakeService{}})
	r := do(t, h, call{method: "POST", path: "/accounts:completeSignIn", body: `{"sign_in_completion_token":"opaque"}`})
	s, body := aipError(t, r)
	message, _ := body["message"].(string)
	if s != "INVALID_ARGUMENT/CREDENTIAL_ONEOF" || strings.Contains(message, "wechat") || strings.Contains(message, "apple") {
		t.Fatalf("completion oneof: %s", r.Body.String())
	}
}

func TestSignInCompletionDeviceHeaders(t *testing.T) {
	for _, tc := range []struct {
		headers map[string]string
		want    user.Device
	}{
		{nil, user.Device{}},
		{map[string]string{"X-Device-Id": "device"}, user.Device{ID: "device"}},
		{map[string]string{"X-Device-Name": "Pixel 9"}, user.Device{Name: "Pixel 9"}},
		{devHeaders(), user.Device{ID: devHeaders()["X-Device-Id"], Name: "Pixel 9"}},
	} {
		f := &completionService{fakeService: &fakeService{}, send: func(_ context.Context, _ string, _ enum.IdentityKind, _ string, dev user.Device, _ user.Meta) (user.CodeChallenge, error) {
			if dev != tc.want {
				t.Fatalf("device: %+v want %+v", dev, tc.want)
			}
			return user.CodeChallenge{}, nil
		}, complete: func(_ context.Context, _ string, _ user.CodeCredential, dev user.Device, _ user.Meta) (user.SignInResult, error) {
			if dev != tc.want {
				t.Fatalf("device: %+v want %+v", dev, tc.want)
			}
			return user.SignInResult{Tokens: &user.TokenResult{}}, nil
		}}
		for path, body := range map[string]string{"/accounts:sendSignInCompletionCode": completionCodeBody, "/accounts:completeSignIn": completionBody} {
			r := do(t, completionHandler(t, f), call{method: "POST", path: path, body: body, headers: tc.headers})
			if r.Code != 200 {
				t.Fatalf("device accepted: %d %s", r.Code, r.Body.String())
			}
		}
	}
	h := completionHandler(t, &completionService{fakeService: &fakeService{}})
	for _, headers := range []map[string]string{{"X-Device-Id": strings.Repeat("x", 65)}, {"X-Device-Name": strings.Repeat("x", 65)}, {"X-Device-Name": "line\nfeed"}} {
		for path, body := range map[string]string{"/accounts:sendSignInCompletionCode": completionCodeBody, "/accounts:completeSignIn": completionBody} {
			r := do(t, h, call{method: "POST", path: path, body: body, headers: headers})
			if r.Code != 400 {
				t.Fatalf("device invalid: %d %s", r.Code, r.Body.String())
			}
		}
	}
}
