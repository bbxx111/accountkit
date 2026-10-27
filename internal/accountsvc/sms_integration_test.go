package accountsvc_test

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAliyunSMS 记录 SendSms 请求，从 TemplateParam 取出验证码；limited 时返回服务商频控。
type fakeAliyunSMS struct {
	mu      sync.Mutex
	codes   map[string]string // PhoneNumbers → 最近验证码
	count   int
	limited bool
}

func (f *integrationFixture) aliyunSMS() (*httptest.Server, *fakeAliyunSMS) {
	f.t.Helper()
	fake := &fakeAliyunSMS{codes: map[string]string{}}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.count++
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.PostForm.Get("Action") != "SendSms" || r.PostForm.Get("Signature") == "" {
			w.WriteHeader(400)
			return
		}
		if fake.limited {
			_, _ = io.WriteString(w, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"limited","RequestId":"REQ-LIMIT"}`)
			return
		}
		var param struct{ Code string }
		_ = json.Unmarshal([]byte(r.PostForm.Get("TemplateParam")), &param)
		fake.codes[r.PostForm.Get("PhoneNumbers")] = param.Code
		_, _ = io.WriteString(w, `{"Code":"OK","Message":"OK","BizId":"1","RequestId":"REQ-OK"}`)
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	f.t.Cleanup(s.Close)
	return s, fake
}

func (s *fakeAliyunSMS) code(number string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codes[number]
}

func (s *fakeAliyunSMS) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

func TestServiceIntegrationAliyunSMS(t *testing.T) {
	f := newIntegration(t)
	server, fake := f.aliyunSMS()
	const secret = "integration-aliyun-secret"
	f.secrets = append(f.secrets, secret, "13812345678", "+8613812345678")
	p := f.start(map[string]string{
		"ACCOUNTKIT_ENABLED_IDENTITY_KINDS":               "PHONE,EMAIL",
		"ACCOUNTSVC_SMS_PROVIDER":                         "aliyun",
		"ACCOUNTSVC_SMS_ALIYUN_ACCESS_KEY_ID":             "integration-id",
		"ACCOUNTSVC_SMS_ALIYUN_ACCESS_KEY_SECRET":         secret,
		"ACCOUNTSVC_SMS_ALIYUN_SIGN_NAME":                 "食方",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_SIGN_IN":          "SMS_SIGN_IN",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_BIND":             "SMS_BIND",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_REAUTH":           "SMS_REAUTH",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_SIGN_IN_COMPLETE": "SMS_COMPLETE",
		"ACCOUNTSVC_SMS_ALIYUN_ENDPOINT":                  server.URL + "/",
		"ACCOUNTSVC_SMTP_BRAND":                           "食方",
	})
	// 品牌前缀作用于内置中文邮件的标题与正文开头。
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": "brand@example.test"}, 200)
	m := f.smtp.mail(t)
	f.secrets = append(f.secrets, m.target, m.code)
	if m.subject != "食方登录验证码" || !strings.HasPrefix(m.body, "【食方】您正在进行登录。") {
		t.Fatalf("branded mail: subject=%q body prefix=%q", m.subject, m.body[:min(len(m.body), 40)])
	}
	target := "+8613812345678"
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": target}, 200)
	code := fake.code("13812345678")
	if code == "" {
		t.Fatal("Aliyun fake did not receive the sign-in code")
	}
	f.secrets = append(f.secrets, code)
	out := f.call("POST", p.baseURL+"/v1/users:signInWithCode", "", map[string]any{"kind": "PHONE", "credential": map[string]string{"code_id": f.codeID(target), "target": target, "code": code}}, 200)
	access, _ := out["access_token"].(string)
	refresh, _ := out["refresh_token"].(string)
	f.secrets = append(f.secrets, access, refresh)
	f.introspect(p, access, true)

	// 非中国大陆号码在占用额度前被拒绝，不调用服务商。
	before := fake.requests()
	rejected := f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": "+447911123456"}, 400)
	if errObj, _ := rejected["error"].(map[string]any); errObj["reason"] != "TARGET_NOT_SUPPORTED" {
		t.Fatalf("unsupported target reason: %v", rejected)
	}
	if fake.requests() != before {
		t.Fatal("unsupported target reached the provider")
	}

	// 服务商频控按发送失败处理：503，而不是 429。
	fake.mu.Lock()
	fake.limited = true
	fake.mu.Unlock()
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "PHONE", "target": "+8613812345679"}, 503)
	f.secrets = append(f.secrets, "13812345679")
	p.stop()
}
