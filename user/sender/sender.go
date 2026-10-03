// Package sender 定义验证码投递接口。服务商实现（阿里云/腾讯云短信、SMTP）作为子包在后续 change 加入；
// 本包只含接口与开发环境用的 Log 实现。
package sender

import (
	"context"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// Message 是一条验证码消息。模板按 Purpose 区分文案（登录 / 绑定 / 验证身份），防止社工诱导。
type Message struct {
	Purpose enum.CodePurpose
	Code    string
	TTL     time.Duration
}

// SMSSender 向 E.164 手机号发送验证码。
type SMSSender interface {
	SendSMS(ctx context.Context, e164 string, m Message) error
}

// EmailSender 向邮箱发送验证码。
type EmailSender interface {
	SendEmail(ctx context.Context, address string, m Message) error
}
