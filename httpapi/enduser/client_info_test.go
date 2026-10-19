package enduser_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
)

const clientCodeBody = `{"email":{"target":"a@example.test","code_id":"0123456789abcdef0123456789abcdef","code":"123456"}}`

func TestAuthenticationClientInfoHeaders(t *testing.T) {
	for _, path := range []string{"/users:signInWithCode", "/users:signInWithIdp", "/users:completeSignIn", "/users/me:reauthenticate", "/token"} {
		for _, mode := range []string{"valid", "invalid", "legacy"} {
			t.Run(path+mode, func(t *testing.T) {
				calls := 0
				capture := func(meta user.Meta) {
					calls++
					if meta.IP != "203.0.113.9" || meta.RequestID != "req-test" {
						t.Fatalf("meta: %+v", meta)
					}
					if mode == "valid" && (meta.ClientInfo == nil || meta.ClientInfo.ClientType != enum.ClientNative || meta.ClientInfo.AppName != "Account App") {
						t.Fatalf("missing structured metadata: %+v", meta.ClientInfo)
					}
					if mode == "legacy" && meta.ClientInfo != nil {
						t.Fatalf("invented info: %+v", meta.ClientInfo)
					}
				}
				f := withAuth(&fakeService{}, principal)
				f.signInWithCode = func(_ context.Context, _ user.CodeCredential, _ user.Device, m user.Meta) (user.SignInResult, error) {
					capture(m)
					return user.SignInResult{Tokens: &user.TokenResult{}}, nil
				}
				f.signInWithIdp = func(_ context.Context, _ user.IdpCredential, _ user.Device, m user.Meta) (user.SignInResult, error) {
					capture(m)
					return user.SignInResult{Tokens: &user.TokenResult{}}, nil
				}
				f.reauthenticate = func(_ context.Context, _ user.Principal, _ user.CodeCredential, m user.Meta) (user.TokenResult, error) {
					capture(m)
					return user.TokenResult{}, nil
				}
				f.refresh = func(_ context.Context, _ string, m user.Meta) (user.TokenResult, error) {
					capture(m)
					return user.TokenResult{}, nil
				}
				svc := &completionService{fakeService: f, complete: func(_ context.Context, _ string, _ user.CodeCredential, _ user.Device, m user.Meta) (user.SignInResult, error) {
					capture(m)
					return user.SignInResult{Tokens: &user.TokenResult{}}, nil
				}}
				body := clientCodeBody
				if path == "/users:signInWithIdp" {
					body = `{"wechat":{"code":"idp-code","app_id":"app"}}`
				}
				if path == "/users:completeSignIn" {
					body = completionBody
				}
				if path == "/token" {
					body = `{"grant_type":"refresh_token","refresh_token":"refresh"}`
				}
				headers := devHeaders()
				if mode == "valid" {
					headers["X-Client-Type"] = "NATIVE"
					headers["X-Client-App-Name"] = "Account App"
				}
				if mode == "invalid" {
					headers["X-Client-Type"] = "UNTRUSTED"
				}
				rec := do(t, completionHandler(t, svc), call{method: "POST", path: path, body: body, headers: headers, bearer: "good"})
				if mode == "invalid" {
					if rec.Code != 400 || calls != 0 {
						t.Fatalf("must reject before consuming: %d calls=%d %s", rec.Code, calls, rec.Body.String())
					}
					if path == "/token" {
						var out map[string]any
						decode(t, rec, &out)
						if out["error"] != "invalid_request" {
							t.Fatal(out)
						}
					} else {
						if s, _ := aipError(t, rec); s != "INVALID_ARGUMENT/INVALID_CLIENT_INFO" {
							t.Fatal(s)
						}
					}
				} else if rec.Code != 200 || calls != 1 {
					t.Fatalf("status %d calls=%d %s", rec.Code, calls, rec.Body.String())
				}
			})
		}
	}
}

func TestClientInfoCallbackPrivacy(t *testing.T) {
	const private = "PRIVATE-RAW-UA-and-description"
	for _, path := range []string{"/users:signInWithCode", "/token"} {
		for _, mode := range []string{"normalized", "invalid result", "invalid error", "private error", "domain sentinel", "auth sentinel", "unavailable sentinel"} {
			t.Run(path+mode, func(t *testing.T) {
				var logs bytes.Buffer
				calls := 0
				check := func(m user.Meta) {
					calls++
					if m.ClientInfo == nil || m.ClientInfo.AppName != "Host App" || m.ClientInfo.BrowserName != "" {
						t.Fatalf("not normalized: %+v", m.ClientInfo)
					}
				}
				f := &fakeService{signInWithCode: func(_ context.Context, _ user.CodeCredential, _ user.Device, m user.Meta) (user.SignInResult, error) {
					check(m)
					return user.SignInResult{Tokens: &user.TokenResult{}}, nil
				}, refresh: func(_ context.Context, _ string, m user.Meta) (user.TokenResult, error) {
					check(m)
					return user.TokenResult{}, nil
				}}
				h := newHandlerWithDeps(t, f, func(d *endUserDeps) {
					d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
					d.ClientInfo = func(*http.Request) (*user.ClientInfo, error) {
						switch mode {
						case "normalized":
							return &user.ClientInfo{ClientType: enum.ClientNative, AppName: " Host App ", BrowserName: "excluded"}, nil
						case "invalid result":
							return &user.ClientInfo{AppName: strings.Repeat("x", 65)}, nil
						case "invalid error":
							return nil, fmt.Errorf("%s: %w", private, user.ErrInvalidClientInfo)
						case "auth sentinel":
							return nil, fmt.Errorf("%s: %w", private, user.ErrInvalidToken)
						case "unavailable sentinel":
							return nil, fmt.Errorf("%s: %w", private, user.ErrUnavailable)
						case "domain sentinel":
							return nil, fmt.Errorf("%s: %w", private, user.ErrInvalidArgument)
						default:
							return nil, fmt.Errorf("%s", private)
						}
					}
				})
				body := clientCodeBody
				if path == "/token" {
					body = `{"grant_type":"refresh_token","refresh_token":"refresh"}`
				}
				rec := do(t, h, call{method: "POST", path: path, body: body, headers: devHeaders()})
				want := 500
				if mode == "normalized" {
					want = 200
				}
				if mode == "invalid result" || mode == "invalid error" {
					want = 400
				}
				if rec.Code != want || calls != map[bool]int{true: 1, false: 0}[mode == "normalized"] {
					t.Fatalf("status %d calls %d: %s", rec.Code, calls, rec.Body.String())
				}
				if strings.Contains(logs.String()+rec.Body.String(), private) {
					t.Fatal("callback private text leaked")
				}
			})
		}
	}
}
func TestNonAuthenticationRoutesIgnoreClientInfo(t *testing.T) {
	f := withAuth(&fakeService{}, principal)
	f.sendSignInCode = func(context.Context, enum.IdentityKind, string, user.Meta) (user.CodeChallenge, error) {
		return user.CodeChallenge{}, nil
	}
	f.sendReauthenticationCode = func(context.Context, user.Principal, enum.IdentityKind, string, user.Meta) (user.CodeChallenge, error) {
		return user.CodeChallenge{}, nil
	}
	f.sendBindCode = func(context.Context, user.Principal, enum.IdentityKind, string, user.Meta) (user.CodeChallenge, error) {
		return user.CodeChallenge{}, nil
	}
	f.bindWithCode = func(context.Context, user.Principal, user.CodeCredential, user.Meta) (user.IdentityInfo, bool, error) {
		return user.IdentityInfo{}, false, nil
	}
	f.revoke = func(context.Context, string, user.Meta) error { return nil }
	f.getMe = func(context.Context, string) (user.Me, error) { return user.Me{}, nil }
	f.listSessions = func(context.Context, string, string) ([]user.SessionInfo, error) { return nil, nil }
	h := newHandlerWithDeps(t, f, func(d *endUserDeps) {
		d.ClientInfo = func(*http.Request) (*user.ClientInfo, error) {
			t.Fatal("metadata parser called on non-authentication route")
			return nil, nil
		}
	})
	for _, c := range []call{
		{method: "POST", path: "/users:sendSignInCode", body: `{"channel":"EMAIL","target":"a@example.test"}`},
		{method: "POST", path: "/users/me:sendReauthenticationCode", body: `{"channel":"EMAIL","target":"a@example.test"}`},
		{method: "POST", path: "/users/me:sendBindCode", body: `{"channel":"EMAIL","target":"a@example.test"}`},
		{method: "POST", path: "/users/me/identities", body: clientCodeBody},
		{method: "POST", path: "/revoke", body: `{"token":"refresh"}`},
		{method: "POST", path: replacementPath, body: replacementBody},
		{method: "GET", path: "/users/me"}, {method: "GET", path: "/users/me/sessions"}, {method: "GET", path: "/users/me/sessions/current"},
	} {
		c.bearer = "good"
		c.headers = map[string]string{"X-Client-Type": "INVALID"}
		rec := do(t, h, c)
		if rec.Code != 200 && rec.Code != 503 {
			t.Fatalf("%s: %d %s", c.path, rec.Code, rec.Body.String())
		}
	}
}
func TestSessionClientInfoDTO(t *testing.T) {
	info := &user.ClientInfo{ClientType: enum.ClientWeb, OSName: "Linux", BrowserName: "Firefox", DeviceType: enum.DeviceDesktop}
	f := withAuth(&fakeService{listSessions: func(context.Context, string, string) ([]user.SessionInfo, error) {
		return []user.SessionInfo{{ID: principal.SessionID, ClientInfo: info, ClientInfoUpdateTime: &testNow}, {}}, nil
	}}, principal)
	rec := do(t, newHandler(t, f), call{method: "GET", path: "/users/me/sessions", bearer: "good"})
	var list struct {
		Sessions []map[string]any `json:"sessions"`
	}
	decode(t, rec, &list)
	first := list.Sessions[0]
	ci, ok := first["client_info"].(map[string]any)
	if !ok || len(ci) != 4 || ci["client_type"] != "WEB" || ci["device_type"] != "DESKTOP" || first["client_info_update_time"] != "2026-09-10T12:00:00Z" {
		t.Fatalf("DTO: %v", first)
	}
	if _, ok := first["initial_client_info"]; ok {
		t.Fatal("initial in list")
	}
	if _, ok := list.Sessions[1]["client_info"]; ok {
		t.Fatal("missing metadata must be omitted")
	}
	svc := &currentSessionService{fakeService: f, current: func(context.Context, user.Principal) (user.CurrentSessionInfo, error) {
		return user.CurrentSessionInfo{SessionInfo: user.SessionInfo{ID: principal.SessionID, ClientInfo: info, ClientInfoUpdateTime: &testNow}, InitialClientInfo: &user.ClientInfo{OSName: "Initial OS"}}, nil
	}}
	h, e := enduser.New(enduser.Deps{Users: svc, ClientIP: func(*http.Request) string { return "203.0.113.9" }, RequestID: func(*http.Request) string { return "test" }, ReauthMaxAge: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	rec = do(t, h.Router(), call{method: "GET", path: "/users/me/sessions/current", bearer: "good"})
	var out map[string]any
	decode(t, rec, &out)
	if out["initial_client_info"].(map[string]any)["os_name"] != "Initial OS" || out["client_info"].(map[string]any)["os_name"] != "Linux" {
		t.Fatal(out)
	}
}
