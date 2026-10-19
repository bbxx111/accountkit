// Package codeemail 演示宿主控制验证码邮件的语言和模板，不发送邮件或记录消息内容。
package codeemail

import (
	"bytes"
	"context"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
)

type languageKey struct{}

// WithLanguage 传递宿主已选择的请求语言；不解析 Accept-Language 或读取用户资料。
// 仅支持 zh-CN/en，其他值在 Render 时回退到实例默认语言。
func WithLanguage(ctx context.Context, language string) context.Context {
	return context.WithValue(ctx, languageKey{}, language)
}

// Renderer 的配置和模板在 New 后不再修改，可以被多个请求并发复用。
type Renderer struct {
	defaultLanguage string
	text            *texttemplate.Template
	html            *htmltemplate.Template
}

var _ smtp.Renderer = (*Renderer)(nil)

// New 固定默认语言；缺省或不支持的默认语言使用 zh-CN。
// 模板均为本地常量，不读取环境凭据，也不进行网络 I/O。
func New(defaultLanguage string) *Renderer {
	if defaultLanguage != "en" && defaultLanguage != "zh-CN" {
		defaultLanguage = "zh-CN"
	}
	return &Renderer{
		defaultLanguage: defaultLanguage,
		text: texttemplate.Must(texttemplate.New("email").Parse(`{{define "zh-CN"}}您正在进行{{.Purpose}}。
验证码：{{.Code}}
有效期：{{.Validity}}。
请勿向他人透露验证码。如非本人操作，请忽略此邮件。
{{end}}{{define "en"}}You are requesting to {{.Purpose}}.
Verification code: {{.Code}}
Valid for {{.Validity}}.
Do not share this code. If you did not request it, ignore this email.
{{end}}`)),
		html: htmltemplate.Must(htmltemplate.New("email").Parse(`{{define "zh-CN"}}<p>您正在进行{{.Purpose}}。</p><p>验证码：<strong>{{.Code}}</strong></p><p>有效期：{{.Validity}}。</p><p>请勿向他人透露验证码。如非本人操作，请忽略此邮件。</p>{{end}}{{define "en"}}<p>You are requesting to {{.Purpose}}.</p><p>Verification code: <strong>{{.Code}}</strong></p><p>Valid for {{.Validity}}.</p><p>Do not share this code. If you did not request it, ignore this email.</p>{{end}}`)),
	}
}

// Render 根据本次 context 选择语言，Text/HTML 使用同一份用途、Code 和 TTL 数据。
// 正 TTL 向上取整到分钟；输入错误、取消或渲染失败只返回固定安全错误。
func (r *Renderer) Render(ctx context.Context, m sender.CodeMessage) (smtp.EmailContent, error) {
	if ctx.Err() != nil || m.Code == "" || m.TTL <= 0 {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	for _, digit := range m.Code {
		if digit < '0' || digit > '9' {
			return smtp.EmailContent{}, sender.ErrUnavailable
		}
	}
	language, _ := ctx.Value(languageKey{}).(string)
	if language != "en" && language != "zh-CN" {
		language = r.defaultLanguage
	}
	var purpose string
	switch m.Purpose {
	case enum.PurposeSignIn:
		if language == "en" {
			purpose = "sign in"
		} else {
			purpose = "登录"
		}
	case enum.PurposeBind:
		if language == "en" {
			purpose = "bind an identity"
		} else {
			purpose = "绑定"
		}
	case enum.PurposeReauth:
		if language == "en" {
			purpose = "reauthenticate"
		} else {
			purpose = "重新认证"
		}
	case enum.PurposeSignInComplete:
		if language == "en" {
			purpose = "complete sign-in and link an identity, registering if needed"
		} else {
			purpose = "完成登录并关联身份，未注册时自动注册"
		}
	default:
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	minutes := m.TTL / time.Minute
	if m.TTL%time.Minute != 0 {
		minutes++
	}
	subject, validity := purpose+"验证码", fmt.Sprintf("%d 分钟", minutes)
	if language == "en" {
		unit := "minutes"
		if minutes == 1 {
			unit = "minute"
		}
		subject, validity = purpose+" verification code", fmt.Sprintf("%d %s", minutes, unit)
	}
	data := struct{ Purpose, Code, Validity string }{purpose, m.Code, validity}
	var text, html bytes.Buffer
	if err := r.text.ExecuteTemplate(&text, language, data); err != nil {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	if err := r.html.ExecuteTemplate(&html, language, data); err != nil {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	if ctx.Err() != nil {
		return smtp.EmailContent{}, sender.ErrUnavailable
	}
	return smtp.EmailContent{Subject: subject, Text: text.String(), HTML: html.String()}, nil
}
