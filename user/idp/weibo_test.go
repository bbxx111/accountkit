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

const weiboTestApp, weiboTestApp2 = "1234567890", "1234567891"

// weiboServer answers oauth2/get_token_info with token(r) and 2/users/show.json with profile(r).
func weiboServer(t *testing.T, token, profile func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/oauth2/get_token_info":
			if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("token info must be a form POST without query")
			}
			token(w, r)
		case "/2/users/show.json":
			if r.Method != http.MethodGet {
				t.Error("profile must be GET")
			}
			if profile == nil {
				fmt.Fprint(w, `{"id":2000000001,"idstr":"2000000001"}`)
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

func newTestWeibo(t *testing.T, srv *httptest.Server, logs *bytes.Buffer, observer observability.Observer) *Weibo {
	t.Helper()
	var logger *slog.Logger
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	w, err := NewWeibo(WeiboOptions{Config: WeiboConfig{AppIDs: []string{weiboTestApp, weiboTestApp2}, APIBaseURL: srv.URL}, Logger: logger, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func weiboCredential(app, token string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"app_id":%q,"access_token":%q}`, app, token))
}

func weiboTokenOK(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, `{"uid":2000000001,"appkey":"1234567890","scope":"","create_at":1700000000,"expire_in":157679999}`)
}

func TestWeiboCredentialRejectedBeforeNetwork(t *testing.T) {
	srv, calls := weiboServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("network called") }, nil)
	p := newTestWeibo(t, srv, nil, nil)
	for raw, want := range map[string]error{
		`{"app_id":"1234567890"}`:                                        ErrMalformedCredential,
		`{"app_id":"1234567890","access_token":""}`:                      ErrMalformedCredential,
		`{"app_id":"1234567890","access_token":"t","uid":"2000000001"}`:  ErrMalformedCredential,
		`{"app_id":"1234567890","access_token":"t","refresh_token":"r"}`: ErrMalformedCredential,
		`["1234567890","t"]`:                                             ErrMalformedCredential,
		`{"app_id":"1234567890","access_token":"t"} {}`:                  ErrMalformedCredential,
		`{"app_id":1234567890,"access_token":"t"}`:                       ErrMalformedCredential,
		`{"app_id":"1234567899","access_token":"t"}`:                     ErrAppNotAllowed,
	} {
		if _, err := p.Verify(context.Background(), json.RawMessage(raw)); !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", raw, err, want)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected credential reached Weibo")
	}
	if p.Kind() != enum.IdentityWeibo {
		t.Fatal("kind")
	}
}

func TestWeiboVerify(t *testing.T) {
	srv, calls := weiboServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("access_token") != "synthetic-token" || len(r.PostForm) != 1 {
			t.Error("token form")
		}
		weiboTokenOK(w, r)
	}, nil)
	before := time.Now()
	id, err := newTestWeibo(t, srv, nil, nil).Verify(context.Background(), weiboCredential(weiboTestApp, "synthetic-token"))
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != enum.IdentityWeibo || id.Subject != "uid:2000000001" || id.UnionID != "" || id.OpenIDs != nil || calls.Load() != 2 {
		t.Fatalf("identity=%+v calls=%d", id, calls.Load())
	}
	if d := id.ExpireTime.Sub(before); d < 157679999*time.Second || d > 157679999*time.Second+5*time.Second {
		t.Fatalf("expire time %v", id.ExpireTime)
	}
}

func TestWeiboHugeExpireDoesNotOverflow(t *testing.T) {
	srv, _ := weiboServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"uid":2000000001,"appkey":"1234567890","expire_in":9223372036854775807}`)
	}, nil)
	id, err := newTestWeibo(t, srv, nil, nil).Verify(context.Background(), weiboCredential(weiboTestApp, "t"))
	if err != nil || !id.ExpireTime.IsZero() && !id.ExpireTime.After(time.Now()) {
		t.Fatalf("huge expire_in must not produce a past deadline: %v %v", id.ExpireTime, err)
	}
}

func TestWeiboStringAndNumberFieldsAreEquivalent(t *testing.T) {
	bodies := []string{
		`{"uid":2000000001,"appkey":1234567890,"expire_in":100}`,
		`{"uid":"2000000001","appkey":"1234567890","expire_in":"100"}`,
	}
	var subjects []string
	for _, body := range bodies {
		srv, _ := weiboServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }, nil)
		id, err := newTestWeibo(t, srv, nil, nil).Verify(context.Background(), weiboCredential(weiboTestApp, "t"))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		subjects = append(subjects, id.Subject)
	}
	if subjects[0] != subjects[1] {
		t.Fatalf("subjects differ: %v", subjects)
	}
	// Beyond float64 precision: must be preserved verbatim.
	srv, _ := weiboServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"uid":12345678901234567891,"appkey":"1234567890","expire_in":1}`)
	}, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"idstr":"12345678901234567891"}`) })
	id, err := newTestWeibo(t, srv, nil, nil).Verify(context.Background(), weiboCredential(weiboTestApp, "t"))
	if err != nil || id.Subject != "uid:12345678901234567891" {
		t.Fatalf("large uid: %+v %v", id, err)
	}
}

func TestWeiboDecimal(t *testing.T) {
	for raw, want := range map[string]string{
		`1`: "1", `"1"`: "1", `0`: "0", `"0"`: "0", `-5`: "-5", `"-5"`: "-5",
		`12345678901234567891`: "12345678901234567891",
	} {
		got, err := weiboDecimal(map[string]json.RawMessage{"k": json.RawMessage(raw)}, "k")
		if err != nil || got != want {
			t.Fatalf("%s -> %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{`1.0`, `1e3`, `"1e3"`, `true`, `null`, `{}`, `[]`, `""`, `" 1"`, `"01"`, `"-0"`, `"+1"`, `"123456789012345678901"`, `"abc"`} {
		if _, err := weiboDecimal(map[string]json.RawMessage{"k": json.RawMessage(raw)}, "k"); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
	if got, err := weiboDecimal(map[string]json.RawMessage{}, "k"); err != nil || got != "" {
		t.Fatal("absent field must be empty without error")
	}
}

func TestWeiboTokenClassification(t *testing.T) {
	errorBody := func(code string) string {
		return `{"error":"expired_token secret-upstream-text","error_code":` + code + `,"request":"/oauth2/get_token_info"}`
	}
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"used", 400, errorBody("21314"), ErrInvalidCredential},
		{"expired", 400, errorBody("21315"), ErrInvalidCredential},
		{"revoked", 400, errorBody("21316"), ErrInvalidCredential},
		{"rejected", 400, errorBody("21317"), ErrInvalidCredential},
		{"authorization revoked", 403, errorBody("21319"), ErrInvalidCredential},
		{"expired token", 400, errorBody("21327"), ErrInvalidCredential},
		{"invalid token string code", 401, errorBody(`"21332"`), ErrInvalidCredential},
		{"error code on 200", 200, errorBody("21327"), ErrInvalidCredential},
		{"higher authorization", 403, errorBody("10005"), ErrMisconfigured},
		{"app restricted", 403, errorBody("10014"), ErrMisconfigured},
		{"unaudited app limit", 403, errorBody("21321"), ErrMisconfigured},
		{"unknown code", 400, errorBody("20502"), ErrUnavailable},
		{"bad code type", 400, `{"error_code":21327.5}`, ErrUnavailable},
		{"4xx without code", 400, `{"error":"x"}`, ErrUnavailable},
		{"4xx not json", 404, `<html>`, ErrUnavailable},
		{"other app", 200, `{"uid":2000000001,"appkey":"1234567891","expire_in":10}`, ErrAppNotAllowed},
		{"other app bad uid", 200, `{"uid":"a b","appkey":"1234567891","expire_in":10}`, ErrAppNotAllowed},
		{"negative expire", 200, `{"uid":2000000001,"appkey":"1234567890","expire_in":-1}`, ErrInvalidCredential},
		{"missing appkey", 200, `{"uid":2000000001,"expire_in":10}`, ErrMisconfigured},
		{"missing uid", 200, `{"appkey":"1234567890","expire_in":10}`, ErrMisconfigured},
		{"missing expire", 200, `{"uid":2000000001,"appkey":"1234567890"}`, ErrMisconfigured},
		{"zero uid", 200, `{"uid":0,"appkey":"1234567890","expire_in":10}`, ErrMisconfigured},
		{"float uid", 200, `{"uid":2000000001.0,"appkey":"1234567890","expire_in":10}`, ErrMisconfigured},
		{"bool uid", 200, `{"uid":true,"appkey":"1234567890","expire_in":10}`, ErrMisconfigured},
		{"expire overflow", 200, `{"uid":1,"appkey":"1234567890","expire_in":99999999999999999999}`, ErrMisconfigured},
		{"duplicate key", 200, `{"uid":1,"uid":2,"appkey":"1234567890","expire_in":10}`, ErrUnavailable},
		{"trailing", 200, `{"uid":1,"appkey":"1234567890","expire_in":10}{}`, ErrUnavailable},
		{"redirect", 302, ``, ErrUnavailable},
		{"rate limited", 429, errorBody("21314"), ErrUnavailable},
		{"server error", 503, errorBody("21314"), ErrUnavailable},
		{"too large", 200, `{"uid":1,"appkey":"1234567890","expire_in":10,"pad":"` + strings.Repeat("x", 70<<10) + `"}`, ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			srv, calls := weiboServer(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 302 {
					w.Header().Set("Location", "/oauth2/get_token_info?redirected=1")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, nil)
			_, err := newTestWeibo(t, srv, &logs, nil).Verify(context.Background(), weiboCredential(weiboTestApp, "secret-token-value"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("failed token check must not query profile or retry: calls=%d", calls.Load())
			}
			for _, leaked := range []string{"secret-token-value", "secret-upstream-text"} {
				if strings.Contains(err.Error(), leaked) || strings.Contains(logs.String(), leaked) {
					t.Fatalf("leaked %q", leaked)
				}
			}
		})
	}
}

func TestWeiboTransportFailures(t *testing.T) {
	block := make(chan struct{})
	srv, _ := weiboServer(t, func(w http.ResponseWriter, r *http.Request) { <-block }, nil)
	t.Cleanup(func() { close(block) })
	p := newTestWeibo(t, srv, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Verify(ctx, weiboCredential(weiboTestApp, "secret-token-value")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller deadline: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := p.Verify(canceled, weiboCredential(weiboTestApp, "t")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	base := closed.URL
	closed.Close()
	p, _ = NewWeibo(WeiboOptions{Config: WeiboConfig{AppIDs: []string{weiboTestApp}, APIBaseURL: base}})
	_, err := p.Verify(context.Background(), weiboCredential(weiboTestApp, "secret-token-value"))
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret-token-value") || strings.Contains(err.Error(), base) {
		t.Fatalf("transport error: %v", err)
	}
}

func TestWeiboSharedUIDAcrossApps(t *testing.T) {
	srv, _ := weiboServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		app := strings.TrimPrefix(r.PostForm.Get("access_token"), "token-")
		fmt.Fprintf(w, `{"uid":2000000001,"appkey":%q,"expire_in":10}`, app)
	}, nil)
	p := newTestWeibo(t, srv, nil, nil)
	a, errA := p.Verify(context.Background(), weiboCredential(weiboTestApp, "token-"+weiboTestApp))
	b, errB := p.Verify(context.Background(), weiboCredential(weiboTestApp2, "token-"+weiboTestApp2))
	if errA != nil || errB != nil || a.Subject != b.Subject || a.Subject != "uid:2000000001" {
		t.Fatalf("shared uid: %+v %+v %v %v", a, b, errA, errB)
	}
}

func TestWeiboPolicyFingerprint(t *testing.T) {
	fp := func(c WeiboConfig) string {
		p, err := NewWeibo(WeiboOptions{Config: c})
		if err != nil {
			t.Fatal(err)
		}
		return p.PolicyFingerprint()
	}
	base := fp(WeiboConfig{AppIDs: []string{weiboTestApp, weiboTestApp2}})
	if fp(WeiboConfig{AppIDs: []string{weiboTestApp2, weiboTestApp}}) != base {
		t.Fatal("app order changed fingerprint")
	}
	for _, c := range []WeiboConfig{
		{AppIDs: []string{weiboTestApp}},
		{AppIDs: []string{weiboTestApp, weiboTestApp2}, APIBaseURL: "https://proxy.example.test"},
	} {
		if fp(c) == base {
			t.Fatalf("fingerprint ignored %+v", c)
		}
	}
	if _, err := NewWeibo(WeiboOptions{Config: WeiboConfig{AppIDs: []string{"bad"}}}); !errors.Is(err, ErrMisconfigured) {
		t.Fatal("invalid config accepted")
	}
}

func TestWeiboProfileMapping(t *testing.T) {
	cases := []struct {
		name, body, wantName, wantPicture string
	}{
		{"hd avatar", `{"id":2000000001,"idstr":"2000000001","screen_name":"Weibo Nick","name":"Friendly","avatar_hd":"https://tvax1.sinaimg.cn/hd.jpg","avatar_large":"https://tvax1.sinaimg.cn/180.jpg","profile_image_url":"https://tvax1.sinaimg.cn/50.jpg"}`, "Weibo Nick", "https://tvax1.sinaimg.cn/hd.jpg"},
		{"large fallback", `{"id":2000000001,"screen_name":"N","avatar_large":"https://tvax1.sinaimg.cn/180.jpg","profile_image_url":"https://tvax1.sinaimg.cn/50.jpg"}`, "N", "https://tvax1.sinaimg.cn/180.jpg"},
		{"small fallback", `{"idstr":"2000000001","screen_name":"N","avatar_hd":"","profile_image_url":"https://tvax1.sinaimg.cn/50.jpg"}`, "N", "https://tvax1.sinaimg.cn/50.jpg"},
		{"http avatar dropped without fallback", `{"id":2000000001,"screen_name":"N","avatar_hd":"http://tvax1.sinaimg.cn/hd.jpg","avatar_large":"https://tvax1.sinaimg.cn/180.jpg"}`, "N", ""},
		{"schemeless avatar dropped", `{"id":2000000001,"screen_name":"N","avatar_hd":"//tvax1.sinaimg.cn/hd.jpg"}`, "N", ""},
		{"https upper scheme kept", `{"id":2000000001,"avatar_hd":"HTTPS://tvax1.sinaimg.cn/hd.jpg"}`, "", "HTTPS://tvax1.sinaimg.cn/hd.jpg"},
		{"missing fields", `{"id":"2000000001"}`, "", ""},
		{"unknown fields ignored", `{"id":2000000001,"gender":"m","location":"L","description":"D","screen_name":"N"}`, "N", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := weiboServer(t, weiboTokenOK, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("access_token") != "synthetic-token" || q.Get("uid") != "2000000001" || len(q) != 2 {
					t.Error("profile query")
				}
				fmt.Fprint(w, tc.body)
			})
			id, err := newTestWeibo(t, srv, nil, nil).Verify(context.Background(), weiboCredential(weiboTestApp, "synthetic-token"))
			if err != nil || id.Name != tc.wantName || id.Picture != tc.wantPicture || calls.Load() != 2 {
				t.Fatalf("id=%+v err=%v calls=%d", id, err, calls.Load())
			}
		})
	}
}

type weiboTestObserver struct{ degradations atomic.Int32 }

func (o *weiboTestObserver) Observe(_ context.Context, v observability.Observation) {
	if d, ok := v.(observability.DegradationObservation); ok && d.Scenario == observability.DegradationWeiboProfileUnavailable {
		o.degradations.Add(int32(d.Count))
	}
}

func TestWeiboProfileFailuresAllowSignIn(t *testing.T) {
	for name, handler := range map[string]func(http.ResponseWriter, *http.Request){
		"rate limited code": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":"User requests out of rate limit!","error_code":10023,"screen_name":"Leaked"}`)
		},
		"error code on 200": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"error_code":"21335","id":2000000001,"screen_name":"Leaked"}`)
		},
		"http 429":     func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) },
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) },
		"other user": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"id":2000000002,"screen_name":"Leaked"}`)
		},
		"id idstr mismatch": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"id":2000000001,"idstr":"2000000002","screen_name":"Leaked"}`)
		},
		"missing id": func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"screen_name":"Leaked"}`) },
		"bad type": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"id":2000000001,"screen_name":7}`)
		},
		"not json": func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<html>Leaked`) },
		"timeout":  func(w http.ResponseWriter, r *http.Request) { time.Sleep(3500 * time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			rec := &weiboTestObserver{}
			srv, _ := weiboServer(t, weiboTokenOK, handler)
			id, err := newTestWeibo(t, srv, &logs, rec).Verify(context.Background(), weiboCredential(weiboTestApp, "secret-token-value"))
			if err != nil || id.Subject != "uid:2000000001" || id.Name != "" || id.Picture != "" {
				t.Fatalf("profile failure must allow sign-in without profile: %+v %v", id, err)
			}
			if rec.degradations.Load() != 1 {
				t.Fatal("profile failure not observed")
			}
			for _, leaked := range []string{"secret-token-value", "Leaked", "rate limit"} {
				if strings.Contains(logs.String(), leaked) {
					t.Fatalf("log leaked %q", leaked)
				}
			}
		})
	}
	t.Run("missing fields and dropped avatar are not degradation", func(t *testing.T) {
		rec := &weiboTestObserver{}
		srv, _ := weiboServer(t, weiboTokenOK, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"id":2000000001,"avatar_hd":"http://tvax1.sinaimg.cn/hd.jpg"}`)
		})
		if _, err := newTestWeibo(t, srv, nil, rec).Verify(context.Background(), weiboCredential(weiboTestApp, "t")); err != nil || rec.degradations.Load() != 0 {
			t.Fatalf("err=%v degradations=%d", err, rec.degradations.Load())
		}
	})
	t.Run("caller cancel during profile", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		srv, _ := weiboServer(t, weiboTokenOK, func(w http.ResponseWriter, r *http.Request) {
			cancel()
			<-r.Context().Done()
		})
		rec := &weiboTestObserver{}
		if _, err := newTestWeibo(t, srv, nil, rec).Verify(ctx, weiboCredential(weiboTestApp, "t")); !errors.Is(err, context.Canceled) || rec.degradations.Load() != 0 {
			t.Fatalf("cancel must abort verification: %v", err)
		}
	})
}
