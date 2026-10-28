package accountsvc

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
)

const templatesSentinel = "TEMPLATE-SENTINEL-41c9"

func emailTemplatesFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "email-templates.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func emailTemplatesJSON(subjectSuffix string) string {
	entry := `{"subject":"{brand}验证码` + subjectSuffix + `","text":"` + templatesSentinel + ` {code} {ttl_minutes}"}`
	return `{"version":1,"templates":{"SIGN_IN":` + entry + `,"BIND":` + entry + `,"REAUTH":` + entry + `,"SIGN_IN_COMPLETE":` + entry + `}}`
}

func TestConfigEmailTemplates(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_SMTP_BRAND", "食方")

	// 未设置：沿用内置渲染器。
	cfg, err := LoadConfig("serve")
	if err != nil || cfg.EmailTemplates != nil || cfg.EmailTemplatesDigest != "" {
		t.Fatalf("unset: %v %v %q", err, cfg.EmailTemplates, cfg.EmailTemplatesDigest)
	}

	content := emailTemplatesJSON("")
	t.Setenv("ACCOUNTSVC_SMTP_TEMPLATES_FILE", emailTemplatesFile(t, content))
	cfg, err = LoadConfig("serve")
	if err != nil || cfg.EmailTemplates == nil {
		t.Fatalf("valid file: %v", err)
	}
	if len(cfg.EmailTemplatesDigest) != 12 || strings.Trim(cfg.EmailTemplatesDigest, "0123456789abcdef") != "" {
		t.Fatalf("digest %q", cfg.EmailTemplatesDigest)
	}
	got, err := cfg.EmailTemplates.Render(context.Background(), sender.CodeMessage{Purpose: enum.PurposeBind, Code: "654321", TTL: 90 * time.Second})
	if err != nil || got.Subject != "食方验证码" || got.Text != templatesSentinel+" 654321 2" {
		t.Fatalf("render: %v %q", err, got)
	}

	// 品牌为空却引用 {brand}。
	t.Setenv("ACCOUNTSVC_SMTP_BRAND", "")
	if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMTP_TEMPLATES_FILE") {
		t.Fatalf("{brand} without brand: %v", err)
	}
	t.Setenv("ACCOUNTSVC_SMTP_BRAND", "食方")

	dir := t.TempDir()
	for name, path := range map[string]string{
		"missing":      filepath.Join(dir, "absent-"+templatesSentinel+".json"),
		"directory":    dir,
		"empty":        emailTemplatesFile(t, ""),
		"oversized":    emailTemplatesFile(t, strings.Repeat(" ", 64*1024+1)),
		"invalid json": emailTemplatesFile(t, "{"+templatesSentinel),
		"subject code": emailTemplatesFile(t, emailTemplatesJSON("{code}")),
		"partial":      emailTemplatesFile(t, `{"version":1,"templates":{"SIGN_IN":{"subject":"`+templatesSentinel+`","text":"{code}"}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ACCOUNTSVC_SMTP_TEMPLATES_FILE", path)
			_, err := LoadConfig("serve")
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMTP_TEMPLATES_FILE") {
				t.Fatalf("want config error naming the variable, got %v", err)
			}
			if strings.Contains(err.Error(), templatesSentinel) || strings.Contains(err.Error(), dir) {
				t.Fatalf("error echoes path or content: %v", err)
			}
		})
	}

	// EMAIL 显式禁用时与 migrate 一样不读取该设置。
	t.Setenv("ACCOUNTSVC_SMTP_TEMPLATES_FILE", filepath.Join(dir, "absent.json"))
	if _, err := LoadConfig("migrate"); err != nil {
		t.Fatalf("migrate must ignore templates: %v", err)
	}
	t.Setenv("ACCOUNTSVC_ENABLED_IDENTITY_KINDS", "WECHAT")
	t.Setenv("ACCOUNTSVC_REQUIRED_IDENTITY_KINDS", "[]")
	t.Setenv("ACCOUNTSVC_WECHAT_APPS", "test-app:synthetic-provider-secret")
	if cfg, err := LoadConfig("serve"); err != nil || cfg.EmailTemplates != nil {
		t.Fatalf("disabled EMAIL must ignore templates: %v", err)
	}
}

func TestLogEmailTemplates(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// EMAIL 禁用时不记录。
	logEmailTemplates(Config{Library: accountkit.Config{EnabledIdentityKinds: []enum.IdentityKind{enum.IdentityWeChat}}}, logger)
	if buf.Len() != 0 {
		t.Fatalf("EMAIL disabled logged: %s", buf.String())
	}

	logEmailTemplates(Config{}, logger)
	if line := buf.String(); !strings.Contains(line, "level=INFO") || !strings.Contains(line, "email templates: built-in") {
		t.Fatalf("built-in: %q", line)
	}

	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_SMTP_BRAND", "食方")
	t.Setenv("ACCOUNTSVC_SMTP_TEMPLATES_FILE", emailTemplatesFile(t, emailTemplatesJSON("")))
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	logEmailTemplates(cfg, logger)
	line := buf.String()
	if !strings.Contains(line, "level=INFO") || !strings.Contains(line, "email templates: custom") || !strings.Contains(line, "version=1") || !strings.Contains(line, "sha256="+cfg.EmailTemplatesDigest) {
		t.Fatalf("custom: %q", line)
	}
	if strings.Contains(line, templatesSentinel) || strings.Contains(line, "食方") || strings.Contains(line, "email-templates.json") {
		t.Fatalf("log exposes template content or path: %q", line)
	}
}
