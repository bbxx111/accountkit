package idp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/observability"
)

type alipayTransport func(*http.Request) (*http.Response, error)

func (f alipayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func alipayHTTPResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

type alipayObservations struct{ degraded int }

func (o *alipayObservations) Observe(_ context.Context, value observability.Observation) {
	if d, ok := value.(observability.DegradationObservation); ok {
		if string(d.Scenario) != "alipay_profile_unavailable" || d.Count != 1 {
			panic("unexpected degradation")
		}
		o.degraded++
	}
}

func TestAlipayProfile(t *testing.T) {
	for _, tc := range []struct {
		name, token, profile  string
		wantName, wantPicture string
		degraded, requests    int
	}{
		{"matching", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret","refresh_token":"synthetic-refresh-secret"}`, `{"code":"10000","open_id":"open","nick_name":"nickname","avatar":"https://example.com/a.png","user_name":"real-name-secret","display_name":"display-name-secret","email":"private-email"}`, "nickname", "https://example.com/a.png", 0, 2},
		{"unset", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`, `{"code":"10000","open_id":"open","user_name":"real-name-secret","display_name":"display-name-secret"}`, "", "", 0, 2},
		{"name_only", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`, `{"code":"10000","open_id":"open","nick_name":"nickname"}`, "nickname", "", 0, 2},
		{"mismatched_open", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`, `{"code":"10000","open_id":"different","nick_name":"untrusted"}`, "", "", 1, 2},
		{"missing_profile_open", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`, `{"code":"10000","nick_name":"untrusted"}`, "", "", 1, 2},
		{"no_exchange_open", `{"union_id":"union","access_token":"synthetic-access-secret"}`, `{}`, "", "", 1, 1},
		{"no_access_token", `{"union_id":"union","open_id":"open"}`, `{}`, "", "", 1, 1},
		{"wrong_token_type", `{"union_id":"union","open_id":"open","access_token":123}`, `{}`, "", "", 1, 1},
		{"permission", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`, `{"code":"40004","sub_code":"isv.insufficient-user-permissions","sub_msg":"synthetic-access-secret"}`, "", "", 1, 2},
		{"malformed_name", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`, `{"code":"10000","open_id":"open","nick_name":42}`, "", "", 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var logs bytes.Buffer
			observer := &alipayObservations{}
			transport := alipayTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				form, _ := url.ParseQuery(string(body))
				if calls == 1 {
					return alipayHTTPResponse(alipaySigned(t, "alipay_system_oauth_token_response", tc.token)), nil
				}
				if form.Get("method") != "alipay.user.info.share" || form.Get("auth_token") != "synthetic-access-secret" || form.Has("code") || form.Has("grant_type") {
					t.Error("profile uses incorrect protocol")
				}
				signature, _ := base64.StdEncoding.DecodeString(form.Get("sign"))
				form.Del("sign")
				keys := make([]string, 0, len(form))
				for k := range form {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, k+"="+form.Get(k))
				}
				digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
				app, _ := alipayKeys(t)
				if err := rsa.VerifyPKCS1v15(&app.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
					t.Error("profile request signature does not cover auth_token")
				}
				return alipayHTTPResponse(alipaySigned(t, "alipay_user_info_share_response", tc.profile)), nil
			})
			p, err := NewAlipay(AlipayOptions{Config: alipayTestConfig(t, "https://example.com/gateway.do"), HTTP: &http.Client{Transport: transport}, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Observer: observer})
			if err != nil {
				t.Fatal(err)
			}
			id, err := p.Verify(context.Background(), alipayCredential(alipayTestApp))
			if err != nil {
				t.Fatal(err)
			}
			if id.Subject != "unionid:union" || id.Name != tc.wantName || id.Picture != tc.wantPicture || observer.degraded != tc.degraded || calls != tc.requests {
				t.Fatalf("id=%+v degradation=%d requests=%d", id, observer.degraded, calls)
			}
			data, _ := json.Marshal(id)
			for _, secret := range []string{"synthetic-access-secret", "synthetic-refresh-secret", "synthetic-code", "real-name-secret", "display-name-secret", "private-email"} {
				if strings.Contains(string(data)+logs.String(), secret) {
					t.Fatal("secret leaked")
				}
			}
		})
	}
}

func TestAlipayBudgetsAndProfileFailures(t *testing.T) {
	for _, mode := range []string{"timeout", "bad_signature", "unsigned", "invalid_json", "http_failure", "too_large", "http_429", "redirect", "duplicate", "error_response", "outer_cancel", "outer_deadline"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			observer := &alipayObservations{}
			var logs bytes.Buffer
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "outer_deadline" {
				var done context.CancelFunc
				ctx, done = context.WithTimeout(ctx, 40*time.Millisecond)
				defer done()
			}
			transport := alipayTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Error("request has no deadline")
				}
				remaining := time.Until(deadline)
				if mode != "outer_deadline" {
					want := 10 * time.Second
					if calls == 2 {
						want = 3 * time.Second
					}
					if remaining > want || remaining < want-500*time.Millisecond {
						t.Errorf("request %d budget=%v want %v", calls, remaining, want)
					}
				} else if remaining > 40*time.Millisecond {
					t.Error("caller deadline ignored")
				}
				if calls == 1 {
					return alipayHTTPResponse(alipaySigned(t, "alipay_system_oauth_token_response", `{"union_id":"union","open_id":"open","access_token":"synthetic-access-secret"}`)), nil
				}
				switch mode {
				case "outer_cancel":
					cancel()
					return nil, context.Canceled
				case "outer_deadline":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "timeout":
					return nil, context.DeadlineExceeded
				case "bad_signature":
					return alipayHTTPResponse(`{"alipay_user_info_share_response":{"code":"10000","open_id":"open","nick_name":"untrusted"},"sign":"AAAA"}`), nil
				case "unsigned":
					return alipayHTTPResponse(`{"alipay_user_info_share_response":{"code":"10000","open_id":"open"}}`), nil
				case "invalid_json":
					return alipayHTTPResponse("synthetic-private-response"), nil
				case "too_large":
					return alipayHTTPResponse(alipaySigned(t, "alipay_user_info_share_response", `{"code":"10000","open_id":"open","padding":"`+strings.Repeat("a", 64<<10)+`"}`)), nil
				case "http_429", "redirect":
					response := alipayHTTPResponse("synthetic-private-response")
					response.StatusCode = 429
					if mode == "redirect" {
						response.StatusCode = 307
						response.Header.Set("Location", "https://untrusted.invalid")
					}
					return response, nil
				case "duplicate":
					return alipayHTTPResponse(alipaySigned(t, "alipay_user_info_share_response", `{"code":"10000","open_id":"open","nick_name":"one","nick_name":"two"}`)), nil
				case "error_response":
					return alipayHTTPResponse(alipaySigned(t, "error_response", `{"code":"40004","sub_code":"isv.insufficient-user-permissions"}`)), nil
				default:
					return nil, errors.New("synthetic-access-secret synthetic-code-secret")
				}
			})
			p, err := NewAlipay(AlipayOptions{Config: alipayTestConfig(t, "https://example.com/gateway.do"), HTTP: &http.Client{Transport: transport}, Observer: observer, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
			if err != nil {
				t.Fatal(err)
			}
			id, err := p.Verify(ctx, alipayCredential(alipayTestApp))
			if calls != 2 {
				t.Fatalf("profile not attempted: %d", calls)
			}
			if mode == "outer_cancel" || mode == "outer_deadline" {
				if !errors.Is(err, ctx.Err()) || err == nil || id.Subject != "" || observer.degraded != 0 {
					t.Fatalf("outer cancellation swallowed: %+v %v, degraded=%d", id, err, observer.degraded)
				}
			} else if err != nil || id.Subject != "unionid:union" || id.Name != "" || observer.degraded != 1 {
				t.Fatalf("profile failure rejected identity: %+v %v, degraded=%d", id, err, observer.degraded)
			}
			if strings.Contains(logs.String(), "synthetic-") {
				t.Fatal("secret leaked to log")
			}
		})
	}
}

func TestAlipayExchangeTransportFailure(t *testing.T) {
	for _, mode := range []string{"transport", "outer_cancel", "outer_deadline", "response_read"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "outer_deadline" {
				var done context.CancelFunc
				ctx, done = context.WithTimeout(ctx, 20*time.Millisecond)
				defer done()
			}
			p, err := NewAlipay(AlipayOptions{Config: alipayTestConfig(t, "https://example.com/gateway.do"), HTTP: &http.Client{Transport: alipayTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "outer_cancel":
					cancel()
					return nil, context.Canceled
				case "outer_deadline":
					<-r.Context().Done()
					return nil, r.Context().Err()
				case "response_read":
					return &http.Response{StatusCode: 200, Body: alipayBrokenBody{}}, nil
				}
				return nil, &url.Error{Op: "Post", URL: "https://synthetic-code-secret/", Err: fmt.Errorf("synthetic-access-secret")}
			})}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Verify(ctx, alipayCredential(alipayTestApp))
			want := ErrUnavailable
			if ctx.Err() != nil {
				want = ctx.Err()
			}
			if !errors.Is(err, want) || calls != 1 || strings.Contains(err.Error(), "synthetic-") {
				t.Fatalf("unsafe failure: %v calls=%d", err, calls)
			}
		})
	}
}

type alipayBrokenBody struct{}

func (alipayBrokenBody) Read([]byte) (int, error) { return 0, errors.New("synthetic-private-response") }
func (alipayBrokenBody) Close() error             { return nil }
