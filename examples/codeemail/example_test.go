package codeemail_test

import (
	"context"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/examples/codeemail"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
)

func ExampleNew() {
	renderer := codeemail.New("en")
	email, err := smtp.NewWithRenderer(smtp.Config{
		Host: "smtp.example.test", Port: 465, From: "account@example.test", TLSMode: "implicit",
	}, renderer)
	if err != nil {
		panic("invalid synthetic SMTP configuration")
	}
	// 宿主将发送器注入 Deps；此示例只装配，不连接 SMTP 或调用 accountkit.New。
	deps := accountkit.Deps{EmailSender: email}
	fmt.Println(deps.EmailSender != nil)
	// Output: true
}

func ExampleWithLanguage() {
	renderer := codeemail.New("en")
	// 宿主中间件或 Go 入口选择语言；发送器派生的超时 context 会保留该值。
	ctx := codeemail.WithLanguage(context.Background(), "zh-CN")
	content, err := renderer.Render(ctx, sender.CodeMessage{
		Purpose: enum.PurposeSignIn, Code: "042713", TTL: 5 * time.Minute,
	})
	if err != nil {
		panic("synthetic render failed")
	}
	// 仅展示非敏感标题；不输出验证码或正文。
	fmt.Println(content.Subject)
	// Output: 登录验证码
}
