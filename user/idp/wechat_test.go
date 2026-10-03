package idp_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

// fakeWeChat 按 code 返回预设响应，并记录最后一次请求的 query。
func fakeWeChat(t *testing.T, responses map[string]string) (*httptest.Server, *string) {
	t.Helper()
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sns/oauth2/access_token" {
			http.NotFound(w, r)
			return
		}
		lastQuery = r.URL.RawQuery
		code := r.URL.Query().Get("code")
		if code == "boom500" {
			w.WriteHeader(500)
			return
		}
		if code == "garbage" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		body, ok := responses[code]
		if !ok {
			body = `{"errcode":40029,"errmsg":"invalid code"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &lastQuery
}

func TestWeChatVerify(t *testing.T) {
	srv, lastQuery := fakeWeChat(t, map[string]string{
		"good":      `{"access_token":"AT-SECRET","expires_in":7200,"refresh_token":"RT-SECRET","openid":"o_1","scope":"snsapi_userinfo","unionid":"u_union_1"}`,
		"noopenid":  `{"access_token":"AT","unionid":"u_union_noopenid"}`,
		"nounion":   `{"access_token":"AT","openid":"o_2","scope":"snsapi_userinfo"}`,
		"used":      `{"errcode":40163,"errmsg":"code been used"}`,
		"expired":   `{"errcode":42003,"errmsg":"code expired"}`,
		"badappid":  `{"errcode":40013,"errmsg":"invalid appid"}`,
		"badsecret": `{"errcode":40125,"errmsg":"invalid appsecret"}`,
		"busy":      `{"errcode":-1,"errmsg":"system busy"}`,
		"risky":     `{"errcode":40226,"errmsg":"high-risk user"}`,
	})
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	w := idp.NewWeChat([]idp.App{{AppID: "wx1", Secret: "s3cret"}}, srv.URL, srv.Client(), logger)
	ctx := context.Background()

	id, err := w.VerifyWeChat(ctx, "wx1", "good")
	if err != nil || id.Kind != enum.IdentityWeChat || id.Subject != "u_union_1" || id.OpenIDs["wx1"] != "o_1" || id.HintEmail != "" {
		t.Fatalf("good: %+v %v", id, err)
	}
	if !strings.Contains(*lastQuery, "appid=wx1") || !strings.Contains(*lastQuery, "secret=s3cret") || !strings.Contains(*lastQuery, "grant_type=authorization_code") {
		t.Fatalf("query: %s", *lastQuery)
	}

	// M5：微信偶尔返回 unionid 但没有 openid（例如未走网页/小程序授权）；空字符串
	// 不能被当成一个真实 openid 记入 OpenIDs。
	id, err = w.VerifyWeChat(ctx, "wx1", "noopenid")
	if err != nil || id.Subject != "u_union_noopenid" || id.OpenIDs != nil {
		t.Fatalf("empty openid must not be recorded: %+v %v", id, err)
	}

	for code, want := range map[string]error{
		"nope":      idp.ErrInvalidCredential,
		"used":      idp.ErrInvalidCredential,
		"expired":   idp.ErrInvalidCredential,
		"badappid":  idp.ErrMisconfigured,
		"badsecret": idp.ErrMisconfigured,
		"nounion":   idp.ErrMisconfigured,
		"busy":      idp.ErrUnavailable,
		"boom500":   idp.ErrUnavailable,
		"garbage":   idp.ErrUnavailable,
		"":          idp.ErrInvalidCredential,
		"risky":     idp.ErrInvalidCredential,
	} {
		if _, err := w.VerifyWeChat(ctx, "wx1", code); !errors.Is(err, want) {
			t.Errorf("code %q: got %v want %v", code, err, want)
		}
	}
	if _, err := w.VerifyWeChat(ctx, "wx-unknown", "good"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("unknown app: %v", err)
	}
	// 日志不得包含 secret、code、access/refresh token
	for _, forbidden := range []string{"s3cret", "AT-SECRET", "RT-SECRET", "good"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "unionid") || !strings.Contains(logs.String(), "wx1") {
		t.Fatalf("misconfiguration must be logged with app id: %s", logs.String())
	}
}

func TestWeChatNetworkErrorIsUnavailable(t *testing.T) {
	srv, _ := fakeWeChat(t, nil)
	url := srv.URL
	srv.Close()
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	w := idp.NewWeChat([]idp.App{{AppID: "wx1", Secret: "s3cret-NET"}}, url, &http.Client{}, logger)
	_, err := w.VerifyWeChat(context.Background(), "wx1", "code-NET")
	if !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("closed server: %v", err)
	}
	// Verify no URL secrets leak into error or logs
	logStr := logs.String()
	errStr := err.Error()
	for _, forbidden := range []string{"s3cret-NET", "code-NET", "sns/oauth2"} {
		if strings.Contains(errStr, forbidden) {
			t.Fatalf("error string leaked %q: %s", forbidden, errStr)
		}
		if strings.Contains(logStr, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logStr)
		}
	}
}

func TestWeChatNoAppsMeansNotAllowed(t *testing.T) {
	w := idp.NewWeChat(nil, "http://127.0.0.1:1", &http.Client{}, nil)
	if _, err := w.VerifyWeChat(context.Background(), "wx1", "c"); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("disabled: %v", err)
	}
}

// M3：AppSecret 以查询参数带在 access_token 请求 URL 里；跟随一个 3xx 重定向会把它
// （连同 code）原样转发给重定向目标。校验器必须不跟随重定向，把 3xx 当非 200 处理。
func TestWeChatDoesNotFollowRedirects(t *testing.T) {
	var secondHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/sns/oauth2/access_token", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/evil?"+r.URL.RawQuery, http.StatusFound)
	})
	mux.HandleFunc("/evil", func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		_, _ = w.Write([]byte(`{"unionid":"leaked"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	w := idp.NewWeChat([]idp.App{{AppID: "wx1", Secret: "s3cret-redirect"}}, srv.URL, srv.Client(), nil)
	if _, err := w.VerifyWeChat(context.Background(), "wx1", "code-redirect"); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("redirect: got %v want ErrUnavailable", err)
	}
	if secondHits.Load() != 0 {
		t.Fatalf("redirect target must never be requested, got %d hits", secondHits.Load())
	}
}
