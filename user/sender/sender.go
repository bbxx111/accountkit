// Package sender 定义验证码投递接口、显式禁用发送器与开发环境用的 Log 实现。
// 可选 SMTP 实现位于 smtp 子包，宿主也可自行注入其他服务商实现。
package sender

import (
	"context"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// CodeMessage 是一条验证码消息。模板按 Purpose 区分文案（登录 / 绑定 / 验证身份），防止社工诱导。
type CodeMessage struct {
	Purpose enum.CodePurpose
	Code    string
	TTL     time.Duration
}

// SMSCodeSender 向 E.164 手机号发送验证码。
type SMSCodeSender interface {
	SendSMS(ctx context.Context, e164 string, m CodeMessage) error
}

// EmailCodeSender 向邮箱发送验证码。
type EmailCodeSender interface {
	SendEmail(ctx context.Context, address string, m CodeMessage) error
}
