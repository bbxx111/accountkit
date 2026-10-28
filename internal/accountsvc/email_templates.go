package accountsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/emailtemplates"
)

const emailTemplatesSetting = "ACCOUNTSVC_SMTP_TEMPLATES_FILE"

// loadEmailTemplates 仅在 serve 且 EMAIL 启用时调用，须在 SMTPBrand 校验之后。路径为空时保留内置渲染器。
func (c *Config) loadEmailTemplates(path string) error {
	if path == "" {
		return nil
	}
	raw, err := readConfigFile(path, emailTemplatesSetting, emailtemplates.MaxFileBytes)
	if err != nil {
		return err
	}
	renderer, err := emailtemplates.Parse(raw, c.SMTPBrand)
	if err != nil {
		// 解析错误只含字段位置与固定分类，不含文件内容。
		return invalidConfig(emailTemplatesSetting + " (" + err.Error() + ")")
	}
	sum := sha256.Sum256(raw)
	c.EmailTemplates, c.EmailTemplatesDigest = renderer, hex.EncodeToString(sum[:])[:12]
	return nil
}

// logEmailTemplates 在 EMAIL 启用时记录当前生效的邮件文案来源。摘要只用于核对多副本是否加载同一文件，
// 不记录路径、模板原文或渲染结果。
func logEmailTemplates(cfg Config, logger *slog.Logger) {
	if !cfg.identityEnabled(enum.IdentityEmail) {
		return
	}
	if cfg.EmailTemplates == nil {
		logger.Info("accountsvc: email templates: built-in")
		return
	}
	logger.Info("accountsvc: email templates: custom", "version", emailtemplates.Version, "sha256", cfg.EmailTemplatesDigest)
}
