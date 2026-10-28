package accountsvc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 部署方清单替换四种用途的内置文案，按 Accept-Language 选择语言；启动日志只说明来源、版本、摘要与
// 语言列表，不含路径、品牌或文字。
func TestServiceIntegrationEmailTemplates(t *testing.T) {
	f := newIntegration(t)
	const hans, hant, en = "HANS-SENTINEL-2b81", "HANT-SENTINEL-6c17", "EN-SENTINEL-9d04"
	entry := func(brand, marker string) string {
		purpose := func(p string) string {
			return `"` + p + `":{"subject":"{{brand}} ` + p + ` ` + marker + `","heading":"` + p + ` ` + marker + `","intro":"intro ` + marker + ` {{brand}}"}`
		}
		return `{"brand":"` + brand + `","logo_url":"https://static.example.test/logo-` + marker + `.png","common":{"code_label":"code ` + marker + `","expiry":"{{ttl_minutes}} min ` + marker + `","safety":"safety ` + marker + `","footer":"footer ` + marker + `"},"templates":{` + purpose("SIGN_IN") + `,` + purpose("BIND") + `,` + purpose("REAUTH") + `,` + purpose("SIGN_IN_COMPLETE") + `}}`
	}
	manifest := `{"version":1,"default_language":"zh-Hans","languages":{"zh-Hans":` + entry("BRAND-HANS", hans) + `,"zh-Hant":` + entry("BRAND-HANT", hant) + `,"en":` + entry("BRAND-EN", en) + `}}`
	path := filepath.Join(t.TempDir(), "templates-path-sentinel.json")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	// 进程输出（日志）不得出现文字、品牌、Logo 地址或路径。
	f.secrets = append(f.secrets, hans, hant, en, "BRAND-HANS", "BRAND-HANT", "BRAND-EN", "templates-path-sentinel", "static.example.test")

	p := f.start(map[string]string{"ACCOUNTSVC_SMTP_BRAND": "", "ACCOUNTSVC_SMTP_TEMPLATES_FILE": path})
	for i, tc := range []struct{ header, brand, marker string }{
		{"zh-HK", "BRAND-HANT", hant},
		{"en-GB,en;q=0.8", "BRAND-EN", en},
		{"ja", "BRAND-HANS", hans},
		{"", "BRAND-HANS", hans},
	} {
		headers := map[string]string{}
		if tc.header != "" {
			headers["Accept-Language"] = tc.header
		}
		target := "templates" + string(rune('a'+i)) + "@example.test"
		transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:sendSignInCode", "application/json", `{"channel":"EMAIL","target":"`+target+`"}`, headers, 200)
		m := f.smtp.mail(t)
		f.secrets = append(f.secrets, m.target, m.code)
		if m.subject != tc.brand+" SIGN_IN "+tc.marker {
			t.Fatalf("%q: subject %q", tc.header, m.subject)
		}
		wantText := "SIGN_IN " + tc.marker + "\n\nintro " + tc.marker + " " + tc.brand + "\n\ncode " + tc.marker + "\n" + m.code + "\n\n5 min " + tc.marker + "\nsafety " + tc.marker + "\n\nfooter " + tc.marker + "\n"
		if m.body != wantText {
			t.Fatalf("%q: text %q want %q", tc.header, m.body, wantText)
		}
		for _, want := range []string{"<!DOCTYPE html>", ">" + m.code + "</p>", `alt="` + tc.brand + `"`, "logo-" + tc.marker + ".png", "footer " + tc.marker + "</p>"} {
			if !strings.Contains(m.html, want) {
				t.Fatalf("%q: html lacks %q:\n%s", tc.header, want, m.html)
			}
		}
	}
	p.stop()
	out := p.output.String()
	if !strings.Contains(out, "email templates: custom") || !strings.Contains(out, `"sha256":"`) || !strings.Contains(out, `"languages":"zh-Hans,en,zh-Hant"`) {
		t.Fatalf("custom templates were not logged at startup:\n%s", out)
	}

	p = f.start(nil)
	p.stop()
	if !strings.Contains(p.output.String(), "email templates: built-in") {
		t.Fatal("built-in templates were not logged at startup")
	}
}
