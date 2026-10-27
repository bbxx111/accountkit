package accountsvc

import (
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/aliyun"
)

const syntheticAliyunSecret = "synthetic-aliyun-secret"

func aliyunEnvironment(t *testing.T) {
	t.Helper()
	for name, value := range map[string]string{
		"ACCOUNTSVC_SMS_PROVIDER":                         "aliyun",
		"ACCOUNTSVC_SMS_ALIYUN_ACCESS_KEY_ID":             "synthetic-id",
		"ACCOUNTSVC_SMS_ALIYUN_ACCESS_KEY_SECRET":         syntheticAliyunSecret,
		"ACCOUNTSVC_SMS_ALIYUN_SIGN_NAME":                 "食方",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_SIGN_IN":          "SMS_1",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_BIND":             "SMS_2",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_REAUTH":           "SMS_3",
		"ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_SIGN_IN_COMPLETE": "SMS_4",
	} {
		t.Setenv(name, value)
	}
}

func TestConfigSMSProvider(t *testing.T) {
	t.Run("explicit PHONE with provider", func(t *testing.T) {
		serveEnvironment(t)
		aliyunEnvironment(t)
		t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "PHONE,EMAIL")
		cfg, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SMS == nil || cfg.SMS.SignName != "食方" || len(cfg.SMS.Templates) != 4 {
			t.Fatalf("SMS config = %+v", cfg.SMS)
		}
		s, err := smsSender(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := s.(*aliyun.Sender); !ok {
			t.Fatalf("PHONE with provider must inject the Aliyun sender, got %T", s)
		}
	})
	t.Run("explicit PHONE without provider", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "PHONE,EMAIL")
		_, err := LoadConfig("serve")
		if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMS_PROVIDER") {
			t.Fatalf("expected missing SMS provider, got %v", err)
		}
	})
	t.Run("omitted Enabled keeps SMS disabled", func(t *testing.T) {
		serveEnvironment(t)
		aliyunEnvironment(t)
		cfg, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		s, err := smsSender(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := s.(sender.Disabled); !ok {
			t.Fatalf("omitted Enabled must keep SMS disabled, got %T", s)
		}
	})
	t.Run("explicit Enabled without PHONE leaves provider unused", func(t *testing.T) {
		serveEnvironment(t)
		aliyunEnvironment(t)
		t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "EMAIL")
		t.Setenv("ACCOUNTKIT_REQUIRED_IDENTITY_KINDS", "EMAIL")
		cfg, err := LoadConfig("serve")
		if err != nil {
			t.Fatal(err)
		}
		if s, err := smsSender(cfg); err != nil || s != nil {
			t.Fatalf("PHONE disabled must not inject an SMS sender: %T %v", s, err)
		}
	})
	for _, name := range []string{"ACCESS_KEY_ID", "ACCESS_KEY_SECRET", "SIGN_NAME", "TEMPLATE_SIGN_IN", "TEMPLATE_BIND", "TEMPLATE_REAUTH", "TEMPLATE_SIGN_IN_COMPLETE"} {
		t.Run("missing "+name, func(t *testing.T) {
			serveEnvironment(t)
			aliyunEnvironment(t)
			t.Setenv("ACCOUNTSVC_SMS_ALIYUN_"+name, "")
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMS_ALIYUN_"+name) || strings.Contains(err.Error(), syntheticAliyunSecret) {
				t.Fatalf("expected error naming %s without secret, got %v", name, err)
			}
		})
	}
	t.Run("unknown provider", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_SMS_PROVIDER", "tencent")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMS_PROVIDER") {
			t.Fatalf("unknown provider: %v", err)
		}
	})
	t.Run("stray Aliyun variable without provider", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTSVC_SMS_ALIYUN_SIGN_NAME", "食方")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMS_PROVIDER") {
			t.Fatalf("stray variable must be rejected: %v", err)
		}
	})
	t.Run("endpoint rules", func(t *testing.T) {
		serveEnvironment(t)
		aliyunEnvironment(t)
		t.Setenv("ACCOUNTSVC_SMS_ALIYUN_ENDPOINT", "http://127.0.0.1:9/")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMS_ALIYUN_ENDPOINT") {
			t.Fatalf("production must reject HTTP endpoint: %v", err)
		}
		t.Setenv("ACCOUNTSVC_MODE", "development")
		if _, err := LoadConfig("serve"); err != nil {
			t.Fatalf("development allows loopback HTTP endpoint: %v", err)
		}
		t.Setenv("ACCOUNTSVC_SMS_ALIYUN_ENDPOINT", "https://user:"+syntheticAliyunSecret+"@dysms.example/")
		if _, err := LoadConfig("serve"); err == nil || strings.Contains(err.Error(), syntheticAliyunSecret) {
			t.Fatalf("endpoint with userinfo: %v", err)
		}
	})
	t.Run("migrate ignores SMS settings", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTSVC_SMS_PROVIDER", "tencent")
		t.Setenv("ACCOUNTSVC_SMS_ALIYUN_ENDPOINT", "not a url")
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must not parse SMS settings: %v", err)
		}
	})
}
