package sender

import (
	"context"
	"log/slog"
	"strings"
)

// Log 把验证码写入日志。**仅供开发环境**：生产使用它意味着验证码明文进日志且用户收不到消息。
type Log struct {
	Logger     *slog.Logger
	MaskTarget func(string) string
}

// NewLog 构造 Log；target 掩码默认保留首 3 尾 4。
func NewLog(logger *slog.Logger) *Log {
	if logger == nil {
		logger = slog.Default()
	}
	return &Log{Logger: logger, MaskTarget: maskDefault}
}

// SendSMS 实现 SMSSender。
func (l *Log) SendSMS(_ context.Context, e164 string, m Message) error {
	l.Logger.Info("DEV-ONLY sms code", "to", l.MaskTarget(e164), "purpose", m.Purpose.String(), "code", m.Code, "ttl", m.TTL)
	return nil
}

// SendEmail 实现 EmailSender。
func (l *Log) SendEmail(_ context.Context, address string, m Message) error {
	l.Logger.Info("DEV-ONLY email code", "to", l.MaskTarget(address), "purpose", m.Purpose.String(), "code", m.Code, "ttl", m.TTL)
	return nil
}

func maskDefault(s string) string {
	if len(s) <= 7 {
		return strings.Repeat("*", len(s))
	}
	asterisks := 4
	if strings.Contains(s, "@") {
		asterisks = 3
	}
	return s[:3] + strings.Repeat("*", asterisks) + s[len(s)-4:]
}
