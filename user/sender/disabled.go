package sender

import (
	"context"
	"errors"
)

var (
	// ErrDisabled 表示宿主显式关闭该发送渠道。
	ErrDisabled = errors.New("sender: channel not enabled")
	// ErrUnavailable 表示投递依赖不可用。实现不得携带服务端响应或消息中的敏感信息。
	ErrUnavailable = errors.New("sender: delivery unavailable")
	// ErrTargetNotSupported 表示发送器通过可选的 SupportsTarget(e164 string) bool 能力声明不支持该目标
	// （例如只发中国大陆号码）；发码流程在占用验证码轮次、冷却与额度之前返回它。
	ErrTargetNotSupported = errors.New("sender: target not supported")
)

// Disabled 显式禁用短信和邮件渠道，不发送消息，也不伪装成投递成功。
type Disabled struct{}

// Enabled 恒为 false：发码流程据此在占用验证码轮次与额度之前拒绝该渠道。
func (Disabled) Enabled() bool { return false }

// SendSMS 恒返回 ErrDisabled。
func (Disabled) SendSMS(context.Context, string, CodeMessage) error { return ErrDisabled }

// SendEmail 恒返回 ErrDisabled。
func (Disabled) SendEmail(context.Context, string, CodeMessage) error { return ErrDisabled }
