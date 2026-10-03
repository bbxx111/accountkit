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
)

// Disabled 显式禁用短信和邮件渠道，不发送消息，也不伪装成投递成功。
type Disabled struct{}

func (Disabled) Enabled() bool                                    { return false }
func (Disabled) SendSMS(context.Context, string, Message) error   { return ErrDisabled }
func (Disabled) SendEmail(context.Context, string, Message) error { return ErrDisabled }
