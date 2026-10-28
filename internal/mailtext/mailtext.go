// Package mailtext 定义写入邮件标题与纯文本正文的字符规则，供 SMTP 发送器与 accountsvc 模板共用。
// 长度上限由调用方各自施加。
package mailtext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// HeaderText 报告 s 能否写入单行邮件标题：合法 UTF-8、非空白、无首尾空白，且不含控制字符
// （含 CR、LF、TAB）、格式字符（双向覆盖、零宽字符）与行/段分隔符，防止头注入与标题伪装。
func HeaderText(s string) bool {
	if s == "" || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || forbiddenFormat(r) {
			return false
		}
	}
	return true
}

// BodyText 报告 s 能否作为纯文本正文：合法 UTF-8、去除空白后非空；控制字符只允许 LF 与 TAB
// （换行统一为 LF，由传输编码输出 CRLF），且不含格式字符与行/段分隔符。
func BodyText(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || forbiddenFormat(r) {
			return false
		}
	}
	return true
}

func forbiddenFormat(r rune) bool {
	return unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}
