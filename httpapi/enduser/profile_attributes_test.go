package enduser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/internal/profileattributes"
	"github.com/bbxx111/accountkit/user"
)

type profileService struct {
	*fakeService
	getProfile   func(context.Context, string) (user.ProfileAttributesView, error)
	patchProfile func(context.Context, string, user.ProfileAttributesPatch, user.Meta) (user.ProfileAttributesView, error)
}

func (s *profileService) GetProfileAttributes(ctx context.Context, id string) (user.ProfileAttributesView, error) {
	return s.getProfile(ctx, id)
}
func (s *profileService) PatchProfileAttributes(ctx context.Context, id string, patch user.ProfileAttributesPatch, meta user.Meta) (user.ProfileAttributesView, error) {
	return s.patchProfile(ctx, id, patch, meta)
}

func profileHandler(t *testing.T, svc enduser.Service, mutate func(*enduser.Deps)) http.Handler {
	t.Helper()
	d := enduser.Deps{Users: svc, ClientIP: func(*http.Request) string { return "203.0.113.9" }, RequestID: func(*http.Request) string { return "req-profile" }, Now: func() time.Time { return testNow }, ReauthMaxAge: time.Minute, SensitiveOpVerification: true}
	if mutate != nil {
		mutate(&d)
	}
	h, err := enduser.New(d)
	if err != nil {
		t.Fatal(err)
	}
	return h.Router()
}

func TestProfileAttributesEndpoint(t *testing.T) {
	for _, method := range []string{"GET", "PATCH"} {
		for _, tc := range []struct {
			name, scope, bearer, data string
			missing                   bool
			want                      int
		}{
			{name: "full", scope: user.ScopeUser, bearer: "good", data: `{"bio":"你好","private":{"n":9007199254740993}}`, want: 200},
			{name: "empty", scope: user.ScopeUser, bearer: "good", data: `{}`, want: 200},
			{name: "missing token", scope: user.ScopeUser, want: 401},
			{name: "invalid token", scope: user.ScopeUser, bearer: "bad", want: 401},
			{name: "bind", scope: user.ScopeBind, bearer: "good", want: 403},
			{name: "undelete", scope: user.ScopeUndelete, bearer: "good", want: 403},
			{name: "old adapter", scope: user.ScopeUser, bearer: "good", missing: true, want: 503},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				p := principal
				p.Scope = tc.scope
				p.AuthTime = testNow.Add(-24 * time.Hour)
				base := withAuth(&fakeService{}, p)
				calls := 0
				get := func(_ context.Context, id string) (user.ProfileAttributesView, error) {
					calls++
					if id != principal.UserID {
						t.Fatalf("target %q", id)
					}
					return user.ProfileAttributesView{Data: json.RawMessage(tc.data)}, nil
				}
				svc := enduser.Service(&profileService{fakeService: base, getProfile: get, patchProfile: func(c context.Context, id string, patch user.ProfileAttributesPatch, meta user.Meta) (user.ProfileAttributesView, error) {
					if meta.RequestID != "req-profile" || meta.IP != "203.0.113.9" {
						t.Fatalf("meta %+v", meta)
					}
					return get(c, id)
				}})
				if tc.missing {
					svc = base
				}
				rec := do(t, profileHandler(t, svc, nil), call{method: method, path: "/users/me/profile?user_id=u_0000000000001", bearer: tc.bearer, body: `{"profile_attributes":{}}`})
				if rec.Code != tc.want {
					t.Fatalf("status %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
				}
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("missing no-store")
				}
				if tc.want == 401 && rec.Header().Get("WWW-Authenticate") == "" {
					t.Fatal("missing challenge")
				}
				if tc.want == 200 {
					var out map[string]json.RawMessage
					decode(t, rec, &out)
					if len(out) != 2 || string(out["name"]) != `"users/u_0k3f9c2m1xq7z/profile"` || string(out["profile_attributes"]) != tc.data {
						t.Fatalf("dto %s", rec.Body.String())
					}
					if calls != 1 {
						t.Fatalf("calls %d", calls)
					}
				} else if calls != 0 {
					t.Fatal("unauthorized domain access")
				}
				if tc.want == 403 {
					if got, _ := aipError(t, rec); got != "PERMISSION_DENIED/INSUFFICIENT_SCOPE" {
						t.Fatal(got)
					}
				}
				if tc.missing {
					if got, _ := aipError(t, rec); got != "UNAVAILABLE/PROFILE_ATTRIBUTES_NOT_CONFIGURED" {
						t.Fatal(got)
					}
				}
			})
		}
	}
	t.Run("no other user route", func(t *testing.T) {
		rec := do(t, profileHandler(t, withAuth(&fakeService{}, principal), nil), call{method: "GET", path: "/users/u_0000000000001/profile", bearer: "good"})
		if rec.Code != 404 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestProfileAttributesRequest(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantRaw, reason string
		max                         int64
	}{
		{name: "object", body: `{"profile_attributes":{"n":9007199254740993}}`, wantRaw: `{"n":9007199254740993}`},
		{name: "raw whitespace", body: "{\"profile_attributes\": \n { \"n\" : 1e0 } \t}", wantRaw: " \n { \"n\" : 1e0 } \t"},
		{name: "escaped envelope key", body: `{"profile_\u0061ttributes":{}}`, wantRaw: `{}`},
		{name: "empty patch", body: `{"profile_attributes":{}}`, wantRaw: `{}`},
		{name: "unicode", body: `{"profile_attributes":{"bio":"你好😃"}}`, wantRaw: `{"bio":"你好😃"}`},
		{name: "missing", body: `{}`, reason: "MALFORMED_BODY"},
		{name: "empty body", body: ``, reason: "MALFORMED_BODY"},
		{name: "outer null", body: `null`, reason: "MALFORMED_BODY"},
		{name: "outer array", body: `[]`, reason: "MALFORMED_BODY"},
		{name: "duplicate outer", body: `{"profile_attributes":{},"profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "escaped duplicate outer", body: `{"profile_attributes":{},"profile_\u0061ttributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "unknown private key", body: `{"private-unknown":"private-value","profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "readonly name", body: `{"name":"users/u_0000000000001/profile","profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "user id", body: `{"user_id":"u_0000000000001","profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "display name", body: `{"display_name":"private-value","profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "mask", body: `{"update_mask":"bio","profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "inner syntax", body: `{"profile_attributes":{"bio":}}`, reason: "MALFORMED_BODY"},
		{name: "trailing", body: `{"profile_attributes":{}} {}`, reason: "MALFORMED_BODY"},
		{name: "trailing junk", body: `{"profile_attributes":{}} private-value`, reason: "MALFORMED_BODY"},
		{name: "invalid outer unicode", body: `{"profile_\ud800attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "null", body: `{"profile_attributes":null}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "array", body: `{"profile_attributes":[]}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "scalar", body: `{"profile_attributes":"private-value"}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "duplicate inner", body: `{"profile_attributes":{"n":1,"n":2}}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "escaped duplicate inner", body: `{"profile_attributes":{"n":1,"\u006e":2}}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "invalid inner unicode", body: `{"profile_attributes":{"bio":"\ud800"}}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "nul", body: `{"profile_attributes":{"bio":"\u0000"}}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "invalid inner utf8", body: "{\"profile_attributes\":{\"bio\":\"\xff\"}}", reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "raw inner limit", body: `{"profile_attributes":{` + strings.Repeat(" ", 16383) + `}}`, reason: "INVALID_PROFILE_ATTRIBUTES"},
		{name: "raw inner boundary", body: `{"profile_attributes":{` + strings.Repeat(" ", 16382) + `}}`, wantRaw: `{` + strings.Repeat(" ", 16382) + `}`},
		{name: "default outer limit", body: strings.Repeat(" ", 65536) + `{"profile_attributes":{}}`, reason: "MALFORMED_BODY"},
		{name: "custom outer limit", body: `{"profile_attributes":{}}`, max: 23, reason: "MALFORMED_BODY"},
		{name: "custom outer boundary", body: `{"profile_attributes":{}}`, max: 25, wantRaw: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			calls := 0
			svc := &profileService{fakeService: withAuth(&fakeService{}, principal), patchProfile: func(_ context.Context, _ string, p user.ProfileAttributesPatch, _ user.Meta) (user.ProfileAttributesView, error) {
				calls++
				// Exercise the existing real domain decoder; no merge behavior is simulated here.
				if _, err := profileattributes.DecodePatch(p.Document); err != nil {
					return user.ProfileAttributesView{}, user.ErrInvalidArgument
				}
				if string(p.Document) != tc.wantRaw {
					t.Fatalf("raw document changed: %q", p.Document)
				}
				return user.ProfileAttributesView{Data: json.RawMessage(`{}`)}, nil
			}}
			rec := do(t, profileHandler(t, svc, func(d *enduser.Deps) {
				d.MaxBodyBytes = tc.max
				d.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			}), call{method: "PATCH", path: "/users/me/profile", bearer: "good", body: tc.body, headers: map[string]string{"Content-Type": "text/plain"}})
			if tc.reason == "" {
				if rec.Code != 200 || calls != 1 {
					t.Fatalf("status %d calls %d: %s", rec.Code, calls, rec.Body.String())
				}
			} else {
				got, _ := aipError(t, rec)
				if rec.Code != 400 || got != "INVALID_ARGUMENT/"+tc.reason {
					t.Fatalf("status %d %s", rec.Code, got)
				}
				if tc.reason == "MALFORMED_BODY" && calls != 0 {
					t.Fatal("malformed envelope reached domain")
				}
				if strings.Contains(rec.Body.String()+logs.String(), "private-") || strings.Contains(rec.Body.String(), "65536") {
					t.Fatalf("private or fixed-limit error leaked: %s %s", rec.Body.String(), logs.String())
				}
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
}

func TestProfileAttributesErrors(t *testing.T) {
	for _, method := range []string{"GET", "PATCH"} {
		for _, tc := range []struct {
			name   string
			err    error
			want   int
			status string
		}{
			{"schema disabled", fmt.Errorf("private-value: %w", user.ErrProfileAttributesNotConfigured), 503, "UNAVAILABLE/PROFILE_ATTRIBUTES_NOT_CONFIGURED"},
			{"invalid profile", fmt.Errorf("private-value: %w", user.ErrInvalidArgument), 400, "INVALID_ARGUMENT/INVALID_PROFILE_ATTRIBUTES"},
			{"schema violation", &user.ProfileAttributesValidationError{Violations: []user.ProfileAttributesViolation{{Path: "/private-unknown", Rule: "private-value"}}}, 400, "INVALID_ARGUMENT/INVALID_PROFILE_ATTRIBUTES"},
			{"frozen", user.ErrInvalidState, 400, "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE"},
			{"pending deletion", fmt.Errorf("pending: %w", user.ErrInvalidState), 400, "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE"},
			{"deleted", fmt.Errorf("deleted: %w", user.ErrInvalidState), 400, "FAILED_PRECONDITION/INVALID_ACCOUNT_STATE"},
			{"not found", user.ErrNotFound, 404, "NOT_FOUND/NOT_FOUND"},
			{"dependency", fmt.Errorf("private-value: %w", user.ErrUnavailable), 503, "UNAVAILABLE/DEPENDENCY_UNAVAILABLE"},
			{"storage", errors.New("private-value storage or uncertain commit"), 500, "INTERNAL/"},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				var logs bytes.Buffer
				svc := &profileService{fakeService: withAuth(&fakeService{}, principal), getProfile: func(context.Context, string) (user.ProfileAttributesView, error) {
					return user.ProfileAttributesView{}, tc.err
				}, patchProfile: func(context.Context, string, user.ProfileAttributesPatch, user.Meta) (user.ProfileAttributesView, error) {
					return user.ProfileAttributesView{}, tc.err
				}}
				rec := do(t, profileHandler(t, svc, func(d *enduser.Deps) {
					d.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
				}), call{method: method, path: "/users/me/profile", bearer: "good", body: `{"profile_attributes":{}}`})
				got, _ := aipError(t, rec)
				if rec.Code != tc.want || got != tc.status {
					t.Fatalf("status %d %s: %s", rec.Code, got, rec.Body.String())
				}
				if strings.Contains(rec.Body.String()+logs.String(), "private-") {
					t.Fatalf("private error leaked: %s %s", rec.Body.String(), logs.String())
				}
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("missing no-store")
				}
				if tc.want == 500 && !strings.Contains(logs.String(), "req-profile") {
					t.Fatalf("missing request id: %s", logs.String())
				}
			})
		}
	}
	t.Run("capability before parsing", func(t *testing.T) {
		rec := do(t, profileHandler(t, withAuth(&fakeService{}, principal), nil), call{method: "PATCH", path: "/users/me/profile", bearer: "good", body: `private-value`})
		if rec.Code != 503 {
			t.Fatalf("status %d", rec.Code)
		}
	})
}
