package idp_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/user/idp"
)

func TestWeChatApplicationModes(t *testing.T) {
	for _, mini := range []bool{false, true} {
		for _, mode := range []idp.WeChatSubjectMode{idp.WeChatSubjectModeUnionID, idp.WeChatSubjectModeOpenID} {
			t.Run(fmt.Sprintf("mini=%v/%s", mini, mode), func(t *testing.T) {
				hits := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits++
					path, key, absent := "/sns/oauth2/access_token", "code", "js_code"
					if mini {
						path, key, absent = "/sns/jscode2session", "js_code", "code"
					}
					q := r.URL.Query()
					if r.URL.Path != path || q.Get(key) != "credential-secret" || q.Has(absent) || q.Get("appid") != "wx1" || q.Get("secret") != "app-secret" || q.Get("grant_type") != "authorization_code" {
						t.Errorf("unexpected exchange request: %s", r.URL.Path)
					}
					if q.Get(key) == "redirect" {
						http.Redirect(w, r, "/evil", 302)
						return
					}
					_, _ = w.Write([]byte(`{"openid":"open","unionid":"union","session_key":"SESSION-SECRET","access_token":"TOKEN-SECRET"}`))
				}))
				defer srv.Close()
				cfg := idp.WeChatConfig{SubjectMode: mode, BaseURL: srv.URL}
				if mini {
					cfg.MiniProgramApps = []idp.App{{AppID: "wx1", Secret: "app-secret"}}
				} else {
					cfg.Apps = []idp.App{{AppID: "wx1", Secret: "app-secret"}}
				}
				verifier, err := idp.NewWeChatWithConfig(cfg, srv.Client(), nil)
				if err != nil {
					t.Fatal(err)
				}
				got, err := verifier.VerifyWeChat(context.Background(), "wx1", "credential-secret")
				want := "unionid:union"
				if mode == idp.WeChatSubjectModeOpenID {
					want = "openid:wx1:open"
				}
				if err != nil || got.Subject != want || got.UnionID != "union" || got.OpenIDs["wx1"] != "open" {
					t.Fatalf("identity=%+v err=%v", got, err)
				}
				if _, err = verifier.VerifyWeChat(context.Background(), "unknown", "c"); !errors.Is(err, idp.ErrAppNotAllowed) || hits != 1 {
					t.Fatalf("unknown app sent request: %v hits=%d", err, hits)
				}
			})
		}
	}
}

func TestWeChatConfigurationValidation(t *testing.T) {
	valid := idp.App{AppID: "wx1", Secret: "SECRET:VALUE"}
	for name, cfg := range map[string]idp.WeChatConfig{
		"mode": {SubjectMode: "SECRET:VALUE"}, "blank-mode": {SubjectMode: " "},
		"duplicate": {Apps: []idp.App{valid, valid}}, "cross-group": {Apps: []idp.App{valid}, MiniProgramApps: []idp.App{valid}},
		"empty-id": {Apps: []idp.App{{Secret: "SECRET:VALUE"}}}, "empty-secret": {Apps: []idp.App{{AppID: "wx1"}}},
		"colon": {Apps: []idp.App{{AppID: "wx:1", Secret: "SECRET:VALUE"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := idp.NewWeChatWithConfig(cfg, nil, nil)
			if err == nil || strings.Contains(err.Error(), "SECRET:VALUE") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
	if _, err := idp.NewWeChatWithConfig(idp.WeChatConfig{Apps: []idp.App{valid}}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWeChatMissingIdentifiersAndSafeErrors(t *testing.T) {
	for _, mode := range []idp.WeChatSubjectMode{idp.WeChatSubjectModeUnionID, idp.WeChatSubjectModeOpenID} {
		for _, mini := range []bool{false, true} {
			var logs strings.Builder
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				code := r.URL.Query().Get("code")
				if mini {
					code = r.URL.Query().Get("js_code")
				}
				switch code {
				case "missing":
					_, _ = w.Write([]byte(`{}`))
				case "misconfigured":
					_, _ = w.Write([]byte(`{"errcode":40125,"errmsg":"SECRET-UPSTREAM"}`))
				case "busy":
					_, _ = w.Write([]byte(`{"errcode":-1,"errmsg":"SECRET-UPSTREAM"}`))
				case "redirect":
					http.Redirect(w, r, "/evil", 302)
				default:
					t.Error("redirect followed")
				}
			}))
			cfg := idp.WeChatConfig{SubjectMode: mode, BaseURL: srv.URL}
			apps := []idp.App{{AppID: "wx", Secret: "SECRET-APP"}}
			if mini {
				cfg.MiniProgramApps = apps
			} else {
				cfg.Apps = apps
			}
			verifier, err := idp.NewWeChatWithConfig(cfg, srv.Client(), slog.New(slog.NewTextHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			for code, want := range map[string]error{"missing": idp.ErrMisconfigured, "misconfigured": idp.ErrMisconfigured, "busy": idp.ErrUnavailable, "redirect": idp.ErrUnavailable} {
				_, err := verifier.VerifyWeChat(context.Background(), "wx", code)
				if !errors.Is(err, want) {
					t.Errorf("%s: %v", code, err)
				}
			}
			if strings.Contains(logs.String(), "SECRET-") {
				t.Fatalf("secret leaked: %s", logs.String())
			}
			srv.Close()
		}
	}
}

func TestWeChatPolicyIncludesModeAndGroups(t *testing.T) {
	fingerprint := func(cfg idp.WeChatConfig) string {
		t.Helper()
		v, err := idp.NewWeChatWithConfig(cfg, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return v.PolicyFingerprint()
	}
	a := idp.App{AppID: "a", Secret: "s"}
	b := idp.App{AppID: "b", Secret: "s"}
	original := fingerprint(idp.WeChatConfig{Apps: []idp.App{a, b}, BaseURL: "https://api.example"})
	if original != fingerprint(idp.WeChatConfig{Apps: []idp.App{{AppID: "b", Secret: "new"}, a}, BaseURL: "https://api.example", SubjectMode: idp.WeChatSubjectModeUnionID}) {
		t.Fatal("order/secret changed fingerprint")
	}
	for _, cfg := range []idp.WeChatConfig{{Apps: []idp.App{a, b}, BaseURL: "https://api.example", SubjectMode: idp.WeChatSubjectModeOpenID}, {Apps: []idp.App{a}, MiniProgramApps: []idp.App{b}, BaseURL: "https://api.example"}, {Apps: []idp.App{a, b}, BaseURL: "https://other.example"}} {
		if original == fingerprint(cfg) {
			t.Fatal("policy change did not change fingerprint")
		}
	}
}
