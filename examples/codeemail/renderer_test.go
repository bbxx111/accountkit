package codeemail_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/examples/codeemail"
	"github.com/bbxx111/accountkit/user/sender"
)

// 每种用途均需在两种正文中保留用途、实际验证码与向上取整的有效期。
func TestRendererPurposes(t *testing.T) {
	cases := []struct {
		purpose enum.CodePurpose
		zh, en  string
	}{
		{enum.PurposeSignIn, "登录", "sign in"},
		{enum.PurposeBind, "绑定", "bind an identity"},
		{enum.PurposeReauth, "重新认证", "reauthenticate"},
		{enum.PurposeSignInComplete, "完成登录并关联身份，未注册时自动注册", "complete sign-in and link an identity, registering if needed"},
	}
	r := codeemail.New("zh-CN")
	for i, tc := range cases {
		for _, language := range []string{"zh-CN", "en"} {
			t.Run(fmt.Sprintf("purpose_%d/%s", i, language), func(t *testing.T) {
				m := sender.CodeMessage{Purpose: tc.purpose, Code: "042713", TTL: 61 * time.Second}
				content, err := r.Render(codeemail.WithLanguage(t.Context(), language), m)
				if err != nil {
					t.Fatal("valid message rejected")
				}
				purpose, minutes, suffix := tc.zh, "2 分钟", "验证码"
				if language == "en" {
					purpose, minutes, suffix = tc.en, "2 minutes", " verification code"
				}
				if content.Subject != purpose+suffix {
					t.Fatal("incorrect localized subject")
				}
				for _, body := range []string{content.Text, content.HTML} {
					for _, want := range []string{purpose, m.Code, minutes} {
						if !strings.Contains(body, want) {
							t.Fatal("message lost its purpose, code or validity")
						}
					}
				}
				if !strings.Contains(content.HTML, "<p>") {
					t.Fatal("HTML alternative missing")
				}
			})
		}
	}
}

func TestRendererLanguageFallback(t *testing.T) {
	for _, tc := range []struct{ name, defaultLanguage, hint, subject string }{
		{"fixed_english", "en", "", "sign in verification code"},
		{"fixed_chinese", "zh-CN", "", "登录验证码"},
		{"unknown_hint_english", "en", "fr", "sign in verification code"},
		{"unknown_hint_chinese", "zh-CN", "fr", "登录验证码"},
		{"empty_default", "", "", "登录验证码"},
		{"unknown_default", "fr", "", "登录验证码"},
		{"override_default", "en", "zh-CN", "登录验证码"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.hint != "" {
				ctx = codeemail.WithLanguage(ctx, tc.hint)
			}
			content, err := codeemail.New(tc.defaultLanguage).Render(ctx, sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "042713", TTL: time.Minute})
			if err != nil || content.Subject != tc.subject {
				t.Fatal("language selection or fallback incorrect")
			}
		})
	}
}

func TestRendererConcurrentLanguages(t *testing.T) {
	r := codeemail.New("en")
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Go(func() {
			lang, want, forbidden := "en", "sign in", "登录"
			if i%2 == 0 {
				lang, want, forbidden = "zh-CN", "登录", "sign in"
			}
			content, err := r.Render(codeemail.WithLanguage(t.Context(), lang), sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "042713", TTL: time.Minute})
			if err != nil || !strings.Contains(content.Subject, want) || strings.Contains(content.Text, forbidden) || strings.Contains(content.HTML, forbidden) {
				t.Error("concurrent language hint leaked")
			}
		})
	}
	wg.Wait()
	// 请求提示不应改变构造时的默认语言。
	content, err := r.Render(t.Context(), sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "042713", TTL: time.Minute})
	if err != nil || content.Subject != "sign in verification code" {
		t.Fatal("request changed default language")
	}
}

func TestRendererTTL(t *testing.T) {
	for _, tc := range []struct {
		ttl  time.Duration
		want string
	}{
		{time.Nanosecond, "Valid for 1 minute."}, {time.Minute, "Valid for 1 minute."},
		{time.Minute + time.Nanosecond, "Valid for 2 minutes."}, {15 * time.Minute, "Valid for 15 minutes."},
	} {
		content, err := codeemail.New("en").Render(t.Context(), sender.CodeMessage{Purpose: enum.PurposeBind, Code: "042713", TTL: tc.ttl})
		if err != nil || !strings.Contains(content.Text, tc.want) || !strings.Contains(content.HTML, tc.want) {
			t.Fatal("validity was not rounded up consistently")
		}
	}
}

func TestRendererRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message sender.CodeMessage
	}{
		{"unspecified_purpose", sender.CodeMessage{Code: "042713", TTL: time.Minute}},
		{"unknown_purpose", sender.CodeMessage{Purpose: enum.CodePurpose(99), Code: "042713", TTL: time.Minute}},
		{"empty_code", sender.CodeMessage{Purpose: enum.PurposeSignIn, TTL: time.Minute}},
		{"non_digit_code", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "<script>synthetic</script>", TTL: time.Minute}},
		{"unicode_digits", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "１２３４", TTL: time.Minute}},
		{"zero_ttl", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "042713"}},
		{"negative_ttl", sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "042713", TTL: -time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, err := codeemail.New("en").Render(t.Context(), tc.message)
			if err != sender.ErrUnavailable {
				t.Fatal("invalid input must return the fixed safe error")
			}
			if content.Subject != "" || content.Text != "" || content.HTML != "" {
				t.Fatal("invalid input produced message content")
			}
		})
	}
}

func TestRendererCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	content, err := codeemail.New("en").Render(ctx, sender.CodeMessage{Purpose: enum.PurposeSignIn, Code: "042713", TTL: time.Minute})
	if !errors.Is(err, sender.ErrUnavailable) || content.Subject != "" || content.Text != "" || content.HTML != "" {
		t.Fatal("canceled render must return the fixed safe error without content")
	}
}
