package aliyun_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/aliyun"
)

const secret = "synthetic-access-key-secret"

func templates() map[enum.CodePurpose]string {
	return map[enum.CodePurpose]string{
		enum.PurposeSignIn:         "SMS_SIGN_IN",
		enum.PurposeBind:           "SMS_BIND",
		enum.PurposeReauth:         "SMS_REAUTH",
		enum.PurposeSignInComplete: "SMS_COMPLETE",
	}
}

func baseConfig(endpoint string, client *http.Client) aliyun.Config {
	return aliyun.Config{AccessKeyID: "synthetic-id", AccessKeySecret: secret, SignName: "食方", Templates: templates(), Endpoint: endpoint, HTTPClient: client}
}

type fakeAliyun struct {
	*httptest.Server
	mu       sync.Mutex
	requests []url.Values
	methods  []string
	respond  func(w http.ResponseWriter)
}

func newFake(t *testing.T) *fakeAliyun {
	t.Helper()
	f := &fakeAliyun{respond: func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":"OK","Message":"OK","BizId":"1","RequestId":"REQ-OK"}`)
	}}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.requests = append(f.requests, r.PostForm)
		f.methods = append(f.methods, r.Method)
		respond := f.respond
		f.mu.Unlock()
		respond(w)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAliyun) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }

func (f *fakeAliyun) last() (string, url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[len(f.methods)-1], f.requests[len(f.requests)-1]
}

func (f *fakeAliyun) setResponse(fn func(w http.ResponseWriter)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respond = fn
}

// resign 独立按官方规则复算签名，校验发送器签出的请求可被服务端验证。
func resign(method string, form url.Values) string {
	enc := func(s string) string {
		return strings.NewReplacer("+", "%20", "*", "%2A", "%7E", "~").Replace(url.QueryEscape(s))
	}
	keys := make([]string, 0, len(form))
	for k := range form {
		if k != "Signature" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, enc(k)+"="+enc(form.Get(k)))
	}
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(method + "&" + enc("/") + "&" + enc(strings.Join(pairs, "&"))))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func msg(p enum.CodePurpose) sender.CodeMessage {
	return sender.CodeMessage{Purpose: p, Code: "246810", TTL: 5 * time.Minute}
}

func TestNewValidatesConfiguration(t *testing.T) {
	mutate := func(fn func(*aliyun.Config)) aliyun.Config {
		c := baseConfig("", nil)
		c.Templates = templates()
		fn(&c)
		return c
	}
	for name, cfg := range map[string]aliyun.Config{
		"no access key id":  mutate(func(c *aliyun.Config) { c.AccessKeyID = "" }),
		"no secret":         mutate(func(c *aliyun.Config) { c.AccessKeySecret = "" }),
		"no sign name":      mutate(func(c *aliyun.Config) { c.SignName = " " }),
		"missing template":  mutate(func(c *aliyun.Config) { delete(c.Templates, enum.PurposeReauth) }),
		"blank template":    mutate(func(c *aliyun.Config) { c.Templates[enum.PurposeBind] = "" }),
		"http endpoint":     mutate(func(c *aliyun.Config) { c.Endpoint = "http://dysmsapi.example.com/" }),
		"userinfo endpoint": mutate(func(c *aliyun.Config) { c.Endpoint = "https://u:" + secret + "@dysmsapi.example.com/" }),
		"query endpoint":    mutate(func(c *aliyun.Config) { c.Endpoint = "https://dysmsapi.example.com/?a=1" }),
		"fragment endpoint": mutate(func(c *aliyun.Config) { c.Endpoint = "https://dysmsapi.example.com/#x" }),
		"negative timeout":  mutate(func(c *aliyun.Config) { c.Timeout = -time.Second }),
	} {
		_, err := aliyun.New(cfg)
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: error echoes secret: %v", name, err)
		}
	}
	if _, err := aliyun.New(mutate(func(c *aliyun.Config) { c.Endpoint = "http://127.0.0.1:1/" })); err != nil {
		t.Fatalf("loopback HTTP endpoint must be allowed for tests: %v", err)
	}
	// 默认 endpoint，构造不进行网络 I/O。
	if _, err := aliyun.New(mutate(func(*aliyun.Config) {})); err != nil {
		t.Fatalf("default config: %v", err)
	}
}

func TestSendSMSRequest(t *testing.T) {
	f := newFake(t)
	s, err := aliyun.New(baseConfig(f.URL+"/", f.Client()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, p := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		if err := s.SendSMS(ctx, "+8613812345678", msg(p)); err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		method, form := f.last()
		if method != http.MethodPost {
			t.Fatalf("method = %s", method)
		}
		for k, want := range map[string]string{
			"Action": "SendSms", "Version": "2017-05-25", "Format": "JSON", "RegionId": "cn-hangzhou",
			"AccessKeyId": "synthetic-id", "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0",
			"PhoneNumbers": "13812345678", "SignName": "食方", "TemplateCode": templates()[p], "TemplateParam": `{"code":"246810"}`,
		} {
			if form.Get(k) != want {
				t.Fatalf("%v: %s = %q, want %q", p, k, form.Get(k), want)
			}
		}
		if form.Get("SignatureNonce") == "" || !strings.HasSuffix(form.Get("Timestamp"), "Z") {
			t.Fatalf("missing nonce or UTC timestamp: %v", form)
		}
		if form.Get("Signature") != resign(http.MethodPost, form) {
			t.Fatal("signature does not verify")
		}
	}
	if !s.SupportsTarget("+8613812345678") || s.SupportsTarget("+447911123456") || s.SupportsTarget("+86123") {
		t.Fatal("SupportsTarget must accept only mainland 11-digit numbers")
	}
	before := f.count()
	if err := s.SendSMS(ctx, "+447911123456", msg(enum.PurposeSignIn)); !errors.Is(err, sender.ErrTargetNotSupported) || f.count() != before {
		t.Fatalf("non-mainland number: %v requests=%d", err, f.count()-before)
	}
	// 不支持的用途或非法验证码不发请求。
	if err := s.SendSMS(ctx, "+8613812345678", sender.CodeMessage{Purpose: enum.CodePurposeUnspecified, Code: "1", TTL: time.Minute}); err == nil || f.count() != before {
		t.Fatal("invalid message must not be sent")
	}
}

func TestSendSMSErrorsAreUnavailableAndPrivate(t *testing.T) {
	const phone = "13812345678"
	leaky := `"Message":"触发号码 ` + phone + ` 的天级流控 246810"`
	for name, respond := range map[string]func(w http.ResponseWriter){
		"business limit": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Code":"isv.BUSINESS_LIMIT_CONTROL",`+leaky+`,"RequestId":"REQ-LIMIT"}`)
		},
		"server error": func(w http.ResponseWriter) {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, `{"Code":"InternalError",`+leaky+`}`)
		},
		"redirect": func(w http.ResponseWriter) {
			w.Header().Set("Location", "https://elsewhere.example/")
			w.WriteHeader(302)
		},
		"not json": func(w http.ResponseWriter) { _, _ = io.WriteString(w, "<html>"+phone+"</html>") },
		"oversized": func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `{"Code":"OK","Pad":"`+strings.Repeat("x", 70*1024)+`"}`)
		},
		"missing code": func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{"RequestId":"REQ"}`) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.setResponse(respond)
			s, err := aliyun.New(baseConfig(f.URL+"/", f.Client()))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SendSMS(context.Background(), "+86"+phone, msg(enum.PurposeSignIn))
			if !errors.Is(err, sender.ErrUnavailable) {
				t.Fatalf("err = %v, want ErrUnavailable", err)
			}
			for _, secretish := range []string{phone, "246810", secret, "天级流控", "elsewhere"} {
				if strings.Contains(err.Error(), secretish) {
					t.Fatalf("error leaks %q: %v", secretish, err)
				}
			}
			if f.count() != 1 {
				t.Fatalf("must not retry or follow redirects: %d requests", f.count())
			}
		})
	}
	t.Run("provider code is kept for operators", func(t *testing.T) {
		f := newFake(t)
		f.setResponse(func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","RequestId":"REQ-LIMIT"}`)
		})
		s, _ := aliyun.New(baseConfig(f.URL+"/", f.Client()))
		err := s.SendSMS(context.Background(), "+86"+phone, msg(enum.PurposeSignIn))
		if !strings.Contains(err.Error(), "isv.BUSINESS_LIMIT_CONTROL") || !strings.Contains(err.Error(), "REQ-LIMIT") {
			t.Fatalf("provider code/request id missing: %v", err)
		}
	})
}

func TestSendSMSTimeoutAndCancellation(t *testing.T) {
	release := make(chan struct{})
	f := newFake(t)
	f.setResponse(func(w http.ResponseWriter) { <-release })
	t.Cleanup(func() { close(release) })
	cfg := baseConfig(f.URL+"/", f.Client())
	cfg.Timeout = 100 * time.Millisecond
	s, err := aliyun.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendSMS(context.Background(), "+8613812345678", msg(enum.PurposeSignIn)); !errors.Is(err, sender.ErrUnavailable) {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SendSMS(ctx, "+8613812345678", msg(enum.PurposeSignIn)); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation must stay canceled: %v", err)
	}
}

func TestSendSMSFailureLoggingAndGatewayErrors(t *testing.T) {
	const phone = "13812345678"
	f := newFake(t)
	var logs strings.Builder
	cfg := baseConfig(f.URL+"/", f.Client())
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	s, err := aliyun.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 网关签名错误以 400 返回 JSON：错误与日志都应保留错误码与 RequestId。
	f.setResponse(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"Code":"SignatureDoesNotMatch","Message":"bad signature for `+phone+`","RequestId":"REQ-SIG"}`)
	})
	err = s.SendSMS(context.Background(), "+86"+phone, msg(enum.PurposeSignIn))
	if !errors.Is(err, sender.ErrUnavailable) || !strings.Contains(err.Error(), "SignatureDoesNotMatch") || !strings.Contains(err.Error(), "REQ-SIG") {
		t.Fatalf("gateway error: %v", err)
	}
	for _, want := range []string{"SignatureDoesNotMatch", "REQ-SIG", "http_status=400"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log missing %q: %s", want, logs.String())
		}
	}
	for _, private := range []string{phone, "246810", secret, "bad signature"} {
		if strings.Contains(logs.String(), private) || strings.Contains(err.Error(), private) {
			t.Fatalf("leaked %q: %s / %v", private, logs.String(), err)
		}
	}
	// 成功不记录告警。
	logs.Reset()
	f.setResponse(func(w http.ResponseWriter) { _, _ = io.WriteString(w, `{"Code":"OK","RequestId":"REQ-OK"}`) })
	if err := s.SendSMS(context.Background(), "+86"+phone, msg(enum.PurposeSignIn)); err != nil || logs.Len() != 0 {
		t.Fatalf("success: %v %q", err, logs.String())
	}
}

func TestSupportsTargetMobileOnly(t *testing.T) {
	s, err := aliyun.New(baseConfig("", nil))
	if err != nil {
		t.Fatal(err)
	}
	for number, want := range map[string]bool{
		"+8613812345678": true,
		"+8619912345678": true,
		"+8675512345678": false, // 深圳固话，11 位但非手机号
		"+86138123456":   false,
		"+447911123456":  false,
		"+86138123456ab": false,
	} {
		if got := s.SupportsTarget(number); got != want {
			t.Errorf("SupportsTarget(%s) = %v, want %v", number, got, want)
		}
	}
}
