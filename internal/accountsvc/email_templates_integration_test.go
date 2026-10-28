package accountsvc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 部署方模板文件替换四种用途的内置文案；启动日志只说明来源、版本与摘要，不含路径或原文。
func TestServiceIntegrationEmailTemplates(t *testing.T) {
	f := newIntegration(t)
	const subject, body = "SUBJECT-SENTINEL-2b81", "BODY-SENTINEL-9d04"
	entry := `{"subject":"{brand}` + subject + `","text":"` + body + `\n验证码：{code}\n有效期：{ttl_minutes} 分钟。\n"}`
	path := filepath.Join(t.TempDir(), "templates-path-sentinel.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"templates":{"SIGN_IN":`+entry+`,"BIND":`+entry+`,"REAUTH":`+entry+`,"SIGN_IN_COMPLETE":`+entry+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// 进程输出（日志）不得出现模板原文、路径或渲染结果。
	f.secrets = append(f.secrets, subject, body, "templates-path-sentinel")

	p := f.start(map[string]string{"ACCOUNTSVC_SMTP_BRAND": "食方", "ACCOUNTSVC_SMTP_TEMPLATES_FILE": path})
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": "templates@example.test"}, 200)
	m := f.smtp.mail(t)
	f.secrets = append(f.secrets, m.target, m.code)
	// 传输层按 CRLF 写出；夹具的 quoted-printable 解码把硬换行还原为 LF。
	if m.subject != "食方"+subject || m.body != body+"\n验证码："+m.code+"\n有效期：5 分钟。\n" {
		t.Fatalf("templated mail: subject=%q body=%q", m.subject, m.body)
	}
	p.stop()
	out := p.output.String()
	if !strings.Contains(out, "email templates: custom") || !strings.Contains(out, "sha256") {
		t.Fatalf("custom templates were not logged at startup:\n%s", out)
	}

	p = f.start(nil)
	p.stop()
	if !strings.Contains(p.output.String(), "email templates: built-in") {
		t.Fatal("built-in templates were not logged at startup")
	}
}
