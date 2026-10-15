package smtp

import (
	"context"
	"fmt"
	"math"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
)

// LegacyChineseRenderer 保留 New 使用的既有四用途中文纯文本邮件。
type LegacyChineseRenderer struct{}

// Render 生成兼容中文文案；正有效期向上取整到分钟。
func (LegacyChineseRenderer) Render(ctx context.Context, m sender.CodeMessage) (EmailContent, error) {
	if ctx.Err() != nil || !validCodeMessage(m) {
		return EmailContent{}, sender.ErrUnavailable
	}
	var purpose string
	switch m.Purpose {
	case enum.PurposeSignIn:
		purpose = "登录"
	case enum.PurposeBind:
		purpose = "绑定"
	case enum.PurposeReauth:
		purpose = "重新认证"
	case enum.PurposeSignInComplete:
		purpose = "完成登录并关联身份，未注册时自动注册"
	}
	return EmailContent{
		Subject: purpose + "验证码",
		Text:    fmt.Sprintf("您正在进行%s。\r\n\r\n验证码：%s\r\n有效期：%.0f 分钟。\r\n\r\n请勿向他人透露验证码。如非本人操作，请忽略此邮件。\r\n", purpose, m.Code, math.Ceil(m.TTL.Minutes())),
	}, nil
}
