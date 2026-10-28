package smtp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/mailtext"
	"github.com/bbxx111/accountkit/user/sender"
)

// MaxBrandLength 是品牌前缀允许的最大 Unicode 码位数。
const MaxBrandLength = 32

// LegacyChineseRenderer 保留 New 使用的既有四用途中文纯文本邮件。Brand 为空时输出与既有文案逐字一致；
// 非空时标题为“<Brand><用途>验证码”，正文以“【<Brand>】”开头。Brand 须先经 ValidateBrand 校验。
type LegacyChineseRenderer struct {
	Brand string
}

// ValidateBrand 校验品牌前缀：空值合法；否则为 1 至 MaxBrandLength 个码位、无控制字符（含换行）、无首尾空白，
// 防止写入邮件标题时造成头注入。
func ValidateBrand(brand string) error {
	if brand == "" {
		return nil
	}
	// 控制字符（含换行）、格式字符（双向覆盖、零宽字符）与行/段分隔符均拒绝，防止头注入与标题伪装。
	if !mailtext.HeaderText(brand) || utf8.RuneCountInString(brand) > MaxBrandLength {
		return errors.New("smtp: invalid Brand")
	}
	return nil
}

// Render 生成兼容中文文案；正有效期向上取整到分钟。品牌不合法时拒绝渲染，不输出未经校验的值。
func (r LegacyChineseRenderer) Render(ctx context.Context, m sender.CodeMessage) (EmailContent, error) {
	if ctx.Err() != nil || !validCodeMessage(m) || ValidateBrand(r.Brand) != nil {
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
	opening := ""
	if r.Brand != "" {
		opening = "【" + r.Brand + "】"
	}
	return EmailContent{
		Subject: r.Brand + purpose + "验证码",
		Text:    opening + fmt.Sprintf("您正在进行%s。\r\n\r\n验证码：%s\r\n有效期：%.0f 分钟。\r\n\r\n请勿向他人透露验证码。如非本人操作，请忽略此邮件。\r\n", purpose, m.Code, math.Ceil(m.TTL.Minutes())),
	}, nil
}
