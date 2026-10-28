package accountsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/emailtemplates"
	"github.com/bbxx111/accountkit/user/sender"
)

const emailTemplatesSetting = "ACCOUNTSVC_SMTP_TEMPLATES_FILE"

// readEmailTemplates 有界读取并解析清单，返回渲染器与内容摘要。错误只含变量名、字段位置与固定分类。
func readEmailTemplates(path string) (*emailtemplates.Renderer, string, error) {
	raw, err := readConfigFile(path, emailTemplatesSetting, emailtemplates.MaxFileBytes)
	if err != nil {
		return nil, "", err
	}
	renderer, err := emailtemplates.Parse(raw)
	if err != nil {
		return nil, "", invalidConfig(emailTemplatesSetting + " (" + err.Error() + ")")
	}
	sum := sha256.Sum256(raw)
	return renderer, hex.EncodeToString(sum[:])[:12], nil
}

// loadEmailTemplates 仅在 serve 且 EMAIL 启用时调用。路径为空时 EmailTemplates 保持 nil，运行时使用
// 嵌入的无品牌默认清单。
func (c *Config) loadEmailTemplates(path string) error {
	if path == "" {
		return nil
	}
	var err error
	c.EmailTemplates, c.EmailTemplatesDigest, err = readEmailTemplates(path)
	return err
}

// logEmailTemplates 在 EMAIL 启用且配置了清单时记录生效的清单。摘要只用于核对多副本是否加载同一清单，
// 不记录路径、品牌、文字或渲染结果。未配置清单的告警由 warnUnconfigured 记录。
func logEmailTemplates(cfg Config, logger *slog.Logger) {
	if !cfg.identityEnabled(enum.IdentityEmail) || cfg.EmailTemplates == nil {
		return
	}
	logger.Info("accountsvc: email templates: custom", "version", emailtemplates.Version, "sha256", cfg.EmailTemplatesDigest, "languages", strings.Join(cfg.EmailTemplates.Languages(), ","))
}

// emailRenderer 返回 serve 实际使用的渲染器：配置的清单，或嵌入的无品牌默认清单。
func emailRenderer(cfg Config) *emailtemplates.Renderer {
	if cfg.EmailTemplates != nil {
		return cfg.EmailTemplates
	}
	return emailtemplates.Default()
}

// acceptLanguage 把请求的 Accept-Language 偏好放进 context，供多语言清单在发送邮件时选择语言。
// 超长或非法的请求头被忽略，请求本身不受影响。
func acceptLanguage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefs := emailtemplates.ParseAcceptLanguage(r.Header.Get("Accept-Language")); prefs != nil {
			r = r.WithContext(emailtemplates.WithLanguagePreference(r.Context(), prefs))
		}
		next.ServeHTTP(w, r)
	})
}

// previewCode 与 previewTTL 是 check -out 预览使用的示例值。
const (
	previewCode = "123456"
	previewTTL  = 5 * time.Minute
)

// runEmailTemplatesCommand 实现 `email-templates check <清单> [-out 目录]`。它不读取 ACCOUNTSVC_ 环境变量，
// 也不连接任何依赖；校验规则与 serve 相同。
func runEmailTemplatesCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("accountsvc: usage: email-templates check <manifest> [-out <dir>]")
	}
	flags := flag.NewFlagSet("email-templates check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	out := flags.String("out", "", "write rendered previews into this directory")
	// 允许路径在选项之前或之后。
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := flags.Parse(rest); err != nil {
			return errors.New("accountsvc: usage: email-templates check <manifest> [-out <dir>]")
		}
		rest = flags.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) != 1 {
		return errors.New("accountsvc: usage: email-templates check <manifest> [-out <dir>]")
	}
	renderer, digest, err := readEmailTemplates(positional[0])
	if err != nil {
		return err
	}
	if *out != "" {
		if err := writeEmailPreviews(renderer, *out); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "email templates: ok version=%d sha256=%s languages=%s\n", emailtemplates.Version, digest, strings.Join(renderer.Languages(), ","))
	return err
}

var previewPurposes = []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete}

// writeEmailPreviews 为每种语言、每种用途写出 <lang>/<PURPOSE>.html、.txt 与 subjects.txt。
// 目录中只允许存在这些固定文件名，避免误覆盖其他文件。
func writeEmailPreviews(renderer *emailtemplates.Renderer, dir string) error {
	allowed := map[string]bool{"subjects.txt": true}
	for _, tag := range renderer.Languages() {
		allowed[tag] = true
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if !allowed[entry.Name()] {
				return errors.New("accountsvc: preview directory contains other files; choose an empty directory")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("accountsvc: preview directory is not readable")
	}
	var subjects strings.Builder
	for _, tag := range renderer.Languages() {
		if err := os.MkdirAll(filepath.Join(dir, tag), 0o755); err != nil {
			return errors.New("accountsvc: cannot create preview directory")
		}
		for _, purpose := range previewPurposes {
			content, err := renderer.RenderIn(tag, sender.CodeMessage{Purpose: purpose, Code: previewCode, TTL: previewTTL})
			if err != nil {
				return errors.New("accountsvc: preview rendering failed")
			}
			base := filepath.Join(dir, tag, purpose.String())
			if err := os.WriteFile(base+".html", []byte(content.HTML), 0o644); err != nil {
				return errors.New("accountsvc: cannot write preview file")
			}
			if err := os.WriteFile(base+".txt", []byte(content.Text), 0o644); err != nil {
				return errors.New("accountsvc: cannot write preview file")
			}
			fmt.Fprintf(&subjects, "%s %s: %s\n", tag, purpose, content.Subject)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "subjects.txt"), []byte(subjects.String()), 0o644); err != nil {
		return errors.New("accountsvc: cannot write preview file")
	}
	return nil
}
