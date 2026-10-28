package accountsvc

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/emailtemplates"
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

// emailTemplatesJSON 生成一份清单；languages 为标签到品牌的映射，第一个标签为默认语言。
func emailTemplatesJSON(subjectSuffix string, languages ...string) string {
	var blocks []string
	for i := 0; i+1 < len(languages); i += 2 {
		tag, brand := languages[i], languages[i+1]
		purpose := func(p string) string {
			return `"` + p + `":{"subject":"{{brand}} ` + p + subjectSuffix + `","heading":"` + p + ` heading","intro":"` + templatesSentinel + ` ` + p + ` {{brand}}"}`
		}
		blocks = append(blocks, `"`+tag+`":{"brand":"`+brand+`","common":{"code_label":"code","expiry":"{{ttl_minutes}} min","safety":"safety `+templatesSentinel+`"},"templates":{`+purpose("SIGN_IN")+`,`+purpose("BIND")+`,`+purpose("REAUTH")+`,`+purpose("SIGN_IN_COMPLETE")+`}}`)
	}
	return `{"version":1,"default_language":"` + languages[0] + `","languages":{` + strings.Join(blocks, ",") + `}}`
}

func TestConfigEmailTemplates(t *testing.T) {
	serveEnvironment(t)

	// 未设置：EmailTemplates 为 nil，运行时使用嵌入的默认清单。
	cfg, err := LoadConfig("serve")
	if err != nil || cfg.EmailTemplates != nil || cfg.EmailTemplatesDigest != "" || emailRenderer(cfg) != emailtemplates.Default() {
		t.Fatalf("unset: %v %v %q", err, cfg.EmailTemplates, cfg.EmailTemplatesDigest)
	}

	t.Setenv("ACCOUNTSVC_SMTP_TEMPLATES_FILE", emailTemplatesFile(t, emailTemplatesJSON("", "zh-Hans", "食方", "en", "Shifang")))
	cfg, err = LoadConfig("serve")
	if err != nil || cfg.EmailTemplates == nil || emailRenderer(cfg) != cfg.EmailTemplates {
		t.Fatalf("valid manifest: %v", err)
	}
	if len(cfg.EmailTemplatesDigest) != 12 || strings.Trim(cfg.EmailTemplatesDigest, "0123456789abcdef") != "" {
		t.Fatalf("digest %q", cfg.EmailTemplatesDigest)
	}
	ctx := emailtemplates.WithLanguagePreference(context.Background(), emailtemplates.ParseAcceptLanguage("en-GB"))
	got, err := cfg.EmailTemplates.Render(ctx, sender.CodeMessage{Purpose: enum.PurposeBind, Code: "654321", TTL: 90 * time.Second})
	if err != nil || got.Subject != "Shifang BIND" || !strings.Contains(got.Text, templatesSentinel+" BIND Shifang") || !strings.Contains(got.Text, "\n654321\n") || !strings.Contains(got.Text, "2 min") || !strings.Contains(got.HTML, ">654321</p>") {
		t.Fatalf("render: %v %q", err, got)
	}

	dir := t.TempDir()
	for name, path := range map[string]string{
		"missing":      filepath.Join(dir, "absent-"+templatesSentinel+".json"),
		"directory":    dir,
		"empty":        emailTemplatesFile(t, ""),
		"oversized":    emailTemplatesFile(t, strings.Repeat(" ", 64*1024+1)),
		"invalid json": emailTemplatesFile(t, "{"+templatesSentinel),
		"subject code": emailTemplatesFile(t, emailTemplatesJSON("{{code}}", "zh-Hans", "食方")),
		"bad default":  emailTemplatesFile(t, strings.Replace(emailTemplatesJSON("", "zh-Hans", "食方"), `"default_language":"zh-Hans"`, `"default_language":"en"`, 1)),
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

	// EMAIL 禁用或未配置清单时不记录；后者由 warnUnconfigured 告警。
	logEmailTemplates(Config{Library: accountkit.Config{EnabledIdentityKinds: []enum.IdentityKind{enum.IdentityWeChat}}}, logger)
	logEmailTemplates(Config{}, logger)
	if buf.Len() != 0 {
		t.Fatalf("unexpected log: %s", buf.String())
	}

	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_SMTP_TEMPLATES_FILE", emailTemplatesFile(t, emailTemplatesJSON("", "zh-Hans", "食方", "en", "Shifang")))
	cfg, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	logEmailTemplates(cfg, logger)
	line := buf.String()
	for _, want := range []string{"level=INFO", "email templates: custom", "version=1", "sha256=" + cfg.EmailTemplatesDigest, "languages=zh-Hans,en"} {
		if !strings.Contains(line, want) {
			t.Fatalf("custom log lacks %q: %q", want, line)
		}
	}
	if strings.Contains(line, templatesSentinel) || strings.Contains(line, "食方") || strings.Contains(line, "Shifang") || strings.Contains(line, "email-templates.json") {
		t.Fatalf("log exposes content or path: %q", line)
	}
}

func TestAcceptLanguageMiddleware(t *testing.T) {
	var seen []string
	handler := acceptLanguage(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(r.Context())
		cfgRaw := emailTemplatesJSON("", "zh-Hans", "食方", "en", "Shifang")
		renderer, err := emailtemplates.Parse([]byte(cfgRaw))
		if err != nil {
			t.Fatal(err)
		}
		got, err := renderer.Render(r.Context(), sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "1", TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, got.Subject)
		w.WriteHeader(204)
	}))
	for _, header := range []string{"en-US,en;q=0.9", "zh-TW", "", "garbage!!", strings.Repeat("en,", 300)} {
		req := httptest.NewRequest("POST", "/v1/users:sendSignInCode", nil)
		if header != "" {
			req.Header.Set("Accept-Language", header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 204 {
			t.Fatalf("header %q: status %d", header, rec.Code)
		}
	}
	if want := []string{"Shifang SIGN_IN", "食方 SIGN_IN", "食方 SIGN_IN", "食方 SIGN_IN", "食方 SIGN_IN"}; strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("selected subjects %v", seen)
	}
}

func TestEmailTemplatesCheckCommand(t *testing.T) {
	manifest := emailTemplatesFile(t, emailTemplatesJSON("", "zh-Hans", "食方", "en", "Shifang"))
	var out bytes.Buffer
	if err := runEmailTemplatesCommand([]string{"check", manifest}, &out); err != nil || !strings.HasPrefix(out.String(), "email templates: ok version=1 sha256=") || !strings.Contains(out.String(), "languages=zh-Hans,en") {
		t.Fatalf("check: %v %q", err, out.String())
	}

	// 预览：目录不存在时创建；写出固定文件名。
	preview := filepath.Join(t.TempDir(), "preview")
	out.Reset()
	if err := runEmailTemplatesCommand([]string{"check", "-out", preview, manifest}, &out); err != nil {
		t.Fatalf("check -out: %v", err)
	}
	for _, name := range []string{"zh-Hans/SIGN_IN.html", "zh-Hans/SIGN_IN.txt", "en/SIGN_IN_COMPLETE.html", "en/SIGN_IN_COMPLETE.txt", "subjects.txt"} {
		if _, err := os.Stat(filepath.Join(preview, name)); err != nil {
			t.Fatalf("preview file %s: %v", name, err)
		}
	}
	html, _ := os.ReadFile(filepath.Join(preview, "en", "BIND.html"))
	subjects, _ := os.ReadFile(filepath.Join(preview, "subjects.txt"))
	if !strings.Contains(string(html), ">123456</p>") || !strings.Contains(string(html), templatesSentinel+" BIND Shifang") || !strings.Contains(string(subjects), "en BIND: Shifang BIND\n") {
		t.Fatalf("preview content:\n%s\n%s", html, subjects)
	}
	// 重跑可以覆盖自己的文件。
	if err := runEmailTemplatesCommand([]string{"check", manifest, "-out", preview}, &out); err != nil {
		t.Fatalf("check -out rerun: %v", err)
	}
	// 目录里有其他文件时拒绝。
	if err := os.WriteFile(filepath.Join(preview, "notes.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runEmailTemplatesCommand([]string{"check", manifest, "-out", preview}, &out); err == nil || !strings.Contains(err.Error(), "other files") {
		t.Fatalf("dirty preview directory: %v", err)
	}

	// 校验失败：非零退出，错误不回显内容，不写预览。
	bad := emailTemplatesFile(t, emailTemplatesJSON("{{code}}", "zh-Hans", "食方"))
	failed := filepath.Join(t.TempDir(), "failed")
	err := runEmailTemplatesCommand([]string{"check", bad, "-out", failed}, &out)
	if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_SMTP_TEMPLATES_FILE (languages.zh-Hans.templates.SIGN_IN.subject") || strings.Contains(err.Error(), templatesSentinel) {
		t.Fatalf("invalid manifest: %v", err)
	}
	if _, statErr := os.Stat(failed); !os.IsNotExist(statErr) {
		t.Fatal("preview written for invalid manifest")
	}
	for _, args := range [][]string{{}, {"validate", manifest}, {"check"}, {"check", manifest, "extra"}, {"check", "-bogus", manifest}} {
		if err := runEmailTemplatesCommand(args, &out); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("args %v: %v", args, err)
		}
	}
}
