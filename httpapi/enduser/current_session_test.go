package enduser_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
)

type currentSessionService struct {
	*fakeService
	current func(context.Context, user.Principal) (user.CurrentSessionInfo, error)
}

func (s *currentSessionService) GetCurrentSession(c context.Context, p user.Principal) (user.CurrentSessionInfo, error) {
	return s.current(c, p)
}
func TestCurrentSessionEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, scope string
		err         error
		missing     bool
		want        int
	}{
		{name: "user", scope: user.ScopeUser, want: 200}, {name: "bind", scope: user.ScopeBind, want: 200}, {name: "undelete", scope: user.ScopeUndelete, want: 403},
		{name: "missing capability", scope: user.ScopeUser, missing: true, want: 503}, {name: "invalid session", scope: user.ScopeUser, err: user.ErrInvalidToken, want: 401},
		{name: "unavailable", scope: user.ScopeUser, err: user.ErrUnavailable, want: 503}, {name: "internal", scope: user.ScopeUser, err: errors.New("private storage failure"), want: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := principal
			p.Scope = tc.scope
			var svc enduser.Service = withAuth(&fakeService{}, p)
			if !tc.missing {
				svc = &currentSessionService{fakeService: svc.(*fakeService), current: func(_ context.Context, got user.Principal) (user.CurrentSessionInfo, error) {
					if got != p {
						t.Fatalf("principal: %+v", got)
					}
					return user.CurrentSessionInfo{SessionInfo: user.SessionInfo{ID: p.SessionID, DeviceID: "old-device", DeviceName: "Pixel", CreateTime: testNow.Add(-time.Hour), LastUsedTime: testNow}, AuthTime: testNow, AuthenticationIdentities: []user.AuthenticationIdentity{{ID: "i_0k3f9c2m1xq8z", Kind: enum.IdentityWeChat}, {ID: "i_0k3f9c2m1xq7z", Kind: enum.IdentityEmail}}}, tc.err
				}}
			}
			h, e := enduser.New(enduser.Deps{Users: svc, ClientIP: func(*http.Request) string { return "203.0.113.9" }, RequestID: func(*http.Request) string { return "test" }, ReauthMaxAge: time.Minute})
			if e != nil {
				t.Fatal(e)
			}
			rec := do(t, h.Router(), call{method: "GET", path: "/users/me/sessions/current", bearer: "good", headers: map[string]string{"X-Client-Type": "INVALID"}})
			if rec.Code != tc.want {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache control: %v", rec.Header())
			}
			if tc.want == 401 && rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing challenge")
			}
			if tc.missing {
				if s, _ := aipError(t, rec); s != "UNAVAILABLE/SESSION_QUERY_NOT_CONFIGURED" {
					t.Fatal(s)
				}
			}
			if contains(rec.Body.String(), "private storage failure") {
				t.Fatal("private error leaked")
			}
			if tc.want == 200 {
				var out map[string]any
				decode(t, rec, &out)
				if out["name"] != "users/"+p.UserID+"/sessions/"+p.SessionID || out["is_current"] != true || out["auth_time"] != "2026-09-10T12:00:00Z" {
					t.Fatalf("dto: %v", out)
				}
				identities := out["authentication_identities"].([]any)
				if _, exists := out["authentication_identities_known"]; exists {
					t.Fatal("obsolete known field exposed")
				}
				if _, exists := out["initial_client_info"]; exists {
					t.Fatal("obsolete initial client info exposed")
				}
				if len(identities) != 2 || len(identities[0].(map[string]any)) != 2 || identities[0].(map[string]any)["kind"] != "EMAIL" || identities[1].(map[string]any)["kind"] != "WECHAT" || identities[0].(map[string]any)["name"] != "users/"+p.UserID+"/identities/i_0k3f9c2m1xq7z" {
					t.Fatal(identities)
				}
			}
		})
	}
}
