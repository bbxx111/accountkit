package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const qqTestApp, qqTestApp2 = "1100000001", "1100000002"

// qqServer answers /oauth2.0/me with me(r) and /user/get_user_info with profile(r).
func qqServer(t *testing.T, me, profile func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Error("QQ requests must be GET")
		}
		switch r.URL.Path {
		case "/oauth2.0/me":
			me(w, r)
		case "/user/get_user_info":
			if profile == nil {
				fmt.Fprint(w, `{"ret":0,"nickname":"","figureurl_qq_1":""}`)
				return
			}
			profile(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestQQ(t *testing.T, srv *httptest.Server, mode QQSubjectMode, logs *bytes.Buffer) *QQ {
	t.Helper()
	var logger *slog.Logger
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	q, err := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp, qqTestApp2}, SubjectMode: mode, APIBaseURL: srv.URL}, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func qqCredential(app, token string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"app_id":%q,"access_token":%q}`, app, token))
}

func TestQQCredentialRejectedBeforeNetwork(t *testing.T) {
	srv, calls := qqServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("network called") }, nil)
	q := newTestQQ(t, srv, QQSubjectModeUnionID, nil)
	for raw, want := range map[string]error{
		`{"app_id":"1100000001"}`:                                  ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":""}`:                ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":"t","openid":"o"}`:  ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":"t","unionid":"u"}`: ErrMalformedCredential,
		`["1100000001","t"]`:                                       ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":"t"} {}`:            ErrMalformedCredential,
		`{"app_id":1100000001,"access_token":"t"}`:                 ErrMalformedCredential,
		`{"app_id":"1100000009","access_token":"t"}`:               ErrAppNotAllowed,
	} {
		if _, err := q.Verify(context.Background(), json.RawMessage(raw)); !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", raw, err, want)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected credential reached QQ")
	}
	if q.Kind() != enum.IdentityQQ {
		t.Fatal("kind")
	}
}

func TestQQVerifyModes(t *testing.T) {
	for _, mode := range []QQSubjectMode{QQSubjectModeUnionID, QQSubjectModeOpenID} {
		t.Run(string(mode), func(t *testing.T) {
			srv, _ := qqServer(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("access_token") != "synthetic-token" || q.Get("fmt") != "json" {
					t.Error("me query")
				}
				if (q.Get("unionid") == "1") != (mode == QQSubjectModeUnionID) {
					t.Error("unionid parameter must only be sent in UNIONID mode")
				}
				body := `{"client_id":"1100000001","openid":"OPEN-ONE"}`
				if mode == QQSubjectModeUnionID {
					body = `{"client_id":"1100000001","openid":"OPEN-ONE","unionid":"UID_ONE"}`
				}
				fmt.Fprint(w, body)
			}, nil)
			id, err := newTestQQ(t, srv, mode, nil).Verify(context.Background(), qqCredential(qqTestApp, "synthetic-token"))
			if err != nil {
				t.Fatal(err)
			}
			want := "unionid:UID_ONE"
			if mode == QQSubjectModeOpenID {
				want = "openid:1100000001:OPEN-ONE"
			}
			if id.Kind != enum.IdentityQQ || id.Subject != want || id.OpenIDs[qqTestApp] != "OPEN-ONE" || len(id.OpenIDs) != 1 {
				t.Fatalf("identity=%+v", id)
			}
			if mode == QQSubjectModeUnionID && id.UnionID != "UID_ONE" {
				t.Fatal("unionid metadata")
			}
		})
	}
}

func TestQQSharedUnionIDAcrossApps(t *testing.T) {
	srv, _ := qqServer(t, func(w http.ResponseWriter, r *http.Request) {
		app := strings.TrimPrefix(r.URL.Query().Get("access_token"), "token-")
		fmt.Fprintf(w, `{"client_id":%q,"openid":"open-%s","unionid":"UID_SHARED"}`, app, app)
	}, nil)
	q := newTestQQ(t, srv, QQSubjectModeUnionID, nil)
	a, errA := q.Verify(context.Background(), qqCredential(qqTestApp, "token-"+qqTestApp))
	b, errB := q.Verify(context.Background(), qqCredential(qqTestApp2, "token-"+qqTestApp2))
	if errA != nil || errB != nil || a.Subject != b.Subject || a.OpenIDs[qqTestApp] == b.OpenIDs[qqTestApp2] {
		t.Fatalf("shared union: %+v %+v %v %v", a, b, errA, errB)
	}
	q = newTestQQ(t, srv, QQSubjectModeOpenID, nil)
	a, _ = q.Verify(context.Background(), qqCredential(qqTestApp, "token-"+qqTestApp))
	b, _ = q.Verify(context.Background(), qqCredential(qqTestApp2, "token-"+qqTestApp2))
	if a.Subject == b.Subject {
		t.Fatal("OPENID mode must isolate apps")
	}
}

func TestQQMeClassification(t *testing.T) {
	cases := []struct {
		name   string
		mode   QQSubjectMode
		status int
		body   string
		want   error
	}{
		{"token check failed", QQSubjectModeUnionID, 200, `{"error":100016,"error_description":"access token check failed"}`, ErrInvalidCredential},
		{"token illegal", QQSubjectModeUnionID, 200, `{"error":100013,"error_description":"x"}`, ErrInvalidCredential},
		{"token expired", QQSubjectModeUnionID, 200, `{"error":100014,"error_description":"x"}`, ErrInvalidCredential},
		{"token revoked", QQSubjectModeUnionID, 200, `{"error":100015,"error_description":"x"}`, ErrInvalidCredential},
		{"unionid not enabled", QQSubjectModeUnionID, 200, `{"error":100048,"error_description":"companyid not set"}`, ErrMisconfigured},
		{"unknown code", QQSubjectModeUnionID, 200, `{"error":100099,"error_description":"x"}`, ErrUnavailable},
		{"non numeric code", QQSubjectModeUnionID, 200, `{"error":"x"}`, ErrUnavailable},
		{"other app", QQSubjectModeUnionID, 200, `{"client_id":"1100000002","openid":"o","unionid":"u"}`, ErrAppNotAllowed},
		{"other app malformed openid", QQSubjectModeOpenID, 200, `{"client_id":"1100000002","openid":"a b"}`, ErrAppNotAllowed},
		{"missing client", QQSubjectModeUnionID, 200, `{"openid":"o","unionid":"u"}`, ErrMisconfigured},
		{"missing unionid", QQSubjectModeUnionID, 200, `{"client_id":"1100000001","openid":"o"}`, ErrMisconfigured},
		{"missing openid", QQSubjectModeOpenID, 200, `{"client_id":"1100000001"}`, ErrMisconfigured},
		{"bad openid", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"a b"}`, ErrMisconfigured},
		{"wrong field type", QQSubjectModeOpenID, 200, `{"client_id":1100000001,"openid":"o"}`, ErrMisconfigured},
		{"jsonp", QQSubjectModeUnionID, 200, `callback( {"client_id":"1100000001","openid":"o","unionid":"u"} );`, ErrUnavailable},
		{"duplicate key", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"o","openid":"p"}`, ErrUnavailable},
		{"trailing", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"o"}{}`, ErrUnavailable},
		{"redirect", QQSubjectModeOpenID, 302, ``, ErrUnavailable},
		{"rate limited", QQSubjectModeOpenID, 429, ``, ErrUnavailable},
		{"server error", QQSubjectModeOpenID, 503, ``, ErrUnavailable},
		{"not found", QQSubjectModeOpenID, 404, ``, ErrUnavailable},
		{"too large", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"o","pad":"` + strings.Repeat("x", 70<<10) + `"}`, ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			srv, calls := qqServer(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 302 {
					w.Header().Set("Location", "/oauth2.0/me?redirected=1")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, nil)
			_, err := newTestQQ(t, srv, tc.mode, &logs).Verify(context.Background(), qqCredential(qqTestApp, "secret-token-value"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("failed /me must not query profile or retry: calls=%d", calls.Load())
			}
			if strings.Contains(err.Error(), "secret-token-value") || strings.Contains(logs.String(), "secret-token-value") || strings.Contains(err.Error(), "access token check failed") {
				t.Fatal("token or upstream text leaked")
			}
		})
	}
}

func TestQQTransportFailures(t *testing.T) {
	block := make(chan struct{})
	srv, _ := qqServer(t, func(w http.ResponseWriter, r *http.Request) { <-block }, nil)
	t.Cleanup(func() { close(block) })
	q := newTestQQ(t, srv, QQSubjectModeOpenID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := q.Verify(ctx, qqCredential(qqTestApp, "secret-token-value")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller deadline: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := q.Verify(canceled, qqCredential(qqTestApp, "t")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	base := closed.URL
	closed.Close()
	q, _ = NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: base}})
	_, err := q.Verify(context.Background(), qqCredential(qqTestApp, "secret-token-value"))
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret-token-value") || strings.Contains(err.Error(), base) {
		t.Fatalf("transport error: %v", err)
	}
}

func TestQQPolicyFingerprint(t *testing.T) {
	fp := func(c QQConfig) string {
		q, err := NewQQ(QQOptions{Config: c})
		if err != nil {
			t.Fatal(err)
		}
		return q.PolicyFingerprint()
	}
	base := fp(QQConfig{AppIDs: []string{qqTestApp, qqTestApp2}})
	if fp(QQConfig{AppIDs: []string{qqTestApp2, qqTestApp}}) != base {
		t.Fatal("app order changed fingerprint")
	}
	for _, c := range []QQConfig{
		{AppIDs: []string{qqTestApp}},
		{AppIDs: []string{qqTestApp, qqTestApp2}, SubjectMode: QQSubjectModeOpenID},
		{AppIDs: []string{qqTestApp, qqTestApp2}, APIBaseURL: "https://proxy.example.test"},
	} {
		if fp(c) == base {
			t.Fatalf("fingerprint ignored %+v", c)
		}
	}
	if _, err := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{"bad"}}}); !errors.Is(err, ErrMisconfigured) {
		t.Fatal("invalid config accepted")
	}
}

func qqMeOK(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, `{"client_id":"1100000001","openid":"OPEN-ONE","unionid":"UID_ONE"}`)
}

func TestQQProfileMapping(t *testing.T) {
	cases := []struct {
		name, body, wantName, wantPicture string
	}{
		{"large avatar", `{"ret":0,"nickname":"QQ Nick","figureurl_qq_1":"https://thirdqq.qlogo.cn/40","figureurl_qq_2":"https://thirdqq.qlogo.cn/100","figureurl_2":"https://qzapp.qlogo.cn/zone"}`, "QQ Nick", "https://thirdqq.qlogo.cn/100"},
		{"small fallback", `{"ret":0,"nickname":"QQ Nick","figureurl_qq_1":"https://thirdqq.qlogo.cn/40"}`, "QQ Nick", "https://thirdqq.qlogo.cn/40"},
		{"empty large falls back", `{"ret":0,"nickname":"N","figureurl_qq_1":"https://thirdqq.qlogo.cn/40","figureurl_qq_2":""}`, "N", "https://thirdqq.qlogo.cn/40"},
		{"listed http upgraded", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://thirdqq.qlogo.cn/g?b=oidb&k=1&s=100"}`, "N", "https://thirdqq.qlogo.cn/g?b=oidb&k=1&s=100"},
		{"listed host case", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://ThirdQQ.QLogo.cn/a"}`, "N", "https://ThirdQQ.QLogo.cn/a"},
		{"unlisted http dropped", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://example.com/a.png"}`, "N", ""},
		{"listed host with port dropped", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://thirdqq.qlogo.cn:8080/a"}`, "N", ""},
		{"userinfo dropped", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://u@thirdqq.qlogo.cn/a"}`, "N", ""},
		{"missing fields", `{"ret":0}`, "", ""},
		{"unknown fields ignored", `{"ret":0,"msg":"","is_lost":0,"gender":"男","province":"P","nickname":"N"}`, "N", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := qqServer(t, qqMeOK, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("access_token") != "synthetic-token" || q.Get("oauth_consumer_key") != qqTestApp || q.Get("openid") != "OPEN-ONE" {
					t.Error("profile query")
				}
				fmt.Fprint(w, tc.body)
			})
			id, err := newTestQQ(t, srv, QQSubjectModeUnionID, nil).Verify(context.Background(), qqCredential(qqTestApp, "synthetic-token"))
			if err != nil || id.Name != tc.wantName || id.Picture != tc.wantPicture || calls.Load() != 2 {
				t.Fatalf("id=%+v err=%v calls=%d", id, err, calls.Load())
			}
		})
	}
}

func TestQQProfileFailuresAllowSignIn(t *testing.T) {
	for name, handler := range map[string]func(http.ResponseWriter, *http.Request){
		"ret nonzero": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ret":1002,"msg":"请先登录","nickname":"Leaked"}`)
		},
		"missing ret": func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"nickname":"Leaked"}`) },
		"is_lost": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ret":0,"is_lost":1,"nickname":"Leaked"}`)
		},
		"bad type":     func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ret":0,"nickname":7}`) },
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) },
		"not json":     func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<html>`) },
		"timeout":      func(w http.ResponseWriter, r *http.Request) { time.Sleep(3500 * time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			rec := &qqTestObserver{}
			srv, _ := qqServer(t, qqMeOK, handler)
			q, err := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: srv.URL}, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Observer: rec})
			if err != nil {
				t.Fatal(err)
			}
			id, err := q.Verify(context.Background(), qqCredential(qqTestApp, "secret-token-value"))
			if err != nil || id.Subject != "unionid:UID_ONE" || id.Name != "" || id.Picture != "" {
				t.Fatalf("profile failure must allow sign-in without profile: %+v %v", id, err)
			}
			if rec.degradations.Load() != 1 {
				t.Fatal("profile failure not observed")
			}
			for _, leaked := range []string{"secret-token-value", "Leaked", "请先登录"} {
				if strings.Contains(logs.String(), leaked) {
					t.Fatalf("log leaked %q", leaked)
				}
			}
		})
	}
	t.Run("missing fields and dropped avatar are not degradation", func(t *testing.T) {
		rec := &qqTestObserver{}
		srv, _ := qqServer(t, qqMeOK, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ret":0,"figureurl_qq_2":"http://example.com/a.png"}`)
		})
		q, _ := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: srv.URL}, Observer: rec})
		if _, err := q.Verify(context.Background(), qqCredential(qqTestApp, "t")); err != nil || rec.degradations.Load() != 0 {
			t.Fatalf("err=%v degradations=%d", err, rec.degradations.Load())
		}
	})
	t.Run("caller cancel during profile", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		srv, _ := qqServer(t, qqMeOK, func(w http.ResponseWriter, r *http.Request) {
			cancel()
			<-r.Context().Done()
		})
		rec := &qqTestObserver{}
		q, _ := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: srv.URL}, Observer: rec})
		if _, err := q.Verify(ctx, qqCredential(qqTestApp, "t")); !errors.Is(err, context.Canceled) || rec.degradations.Load() != 0 {
			t.Fatalf("cancel must abort verification: %v", err)
		}
	})
}

type qqTestObserver struct{ degradations atomic.Int32 }

func (o *qqTestObserver) Observe(_ context.Context, v observability.Observation) {
	if d, ok := v.(observability.DegradationObservation); ok && d.Scenario == observability.DegradationQQProfileUnavailable {
		o.degradations.Add(int32(d.Count))
	}
}

func TestQQAvatarURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                    "",
		"https://any.example.test/a.png":      "https://any.example.test/a.png",
		"http://thirdqq.qlogo.cn/a":           "https://thirdqq.qlogo.cn/a",
		"HTTP://thirdqq.qlogo.cn/a":           "https://thirdqq.qlogo.cn/a",
		"http://qzapp.qlogo.cn/a":             "",
		"http://thirdqq.qlogo.cn.evil.test/a": "",
		"http://thirdqq.qlogo.cn:80/a":        "",
		"ftp://thirdqq.qlogo.cn/a":            "",
		"//thirdqq.qlogo.cn/a":                "",
	} {
		if got := qqAvatarURL(raw); got != want {
			t.Fatalf("%q -> %q, want %q", raw, got, want)
		}
	}
}
