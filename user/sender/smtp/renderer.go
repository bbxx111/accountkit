package smtp

import (
	"context"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
)

// EmailContent 是验证码邮件内容。Subject 与 Text 必须非空白；HTML 可选。
// HTML 中动态值的转义由宿主负责。
type EmailContent struct {
	Subject string
	Text    string
	HTML    string
}

// Renderer 由宿主选择验证码邮件的品牌、用途文案、模板和语言。
// Render 必须支持并发调用并及时响应 context，不能记录验证码或正文。
// 发送器保留 context 值，并让渲染和 SMTP 投递共用一次总超时预算。
type Renderer interface {
	Render(context.Context, sender.CodeMessage) (EmailContent, error)
}

func validCodeMessage(m sender.CodeMessage) bool {
	switch m.Purpose {
	case enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete:
	default:
		return false
	}
	if m.TTL <= 0 || m.Code == "" {
		return false
	}
	for _, digit := range m.Code {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
