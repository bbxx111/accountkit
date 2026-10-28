package mailtext

import (
	"strings"
	"testing"
)

func TestHeaderText(t *testing.T) {
	for _, ok := range []string{"食方", "食方登录验证码", "a b", "{brand}登录验证码"} {
		if !HeaderText(ok) {
			t.Errorf("HeaderText(%q) = false", ok)
		}
	}
	for name, bad := range map[string]string{
		"empty":          "",
		"blank":          "   ",
		"leading space":  " 食方",
		"trailing space": "食方 ",
		"crlf":           "食方\r\nBcc: x@example.org",
		"lf":             "食\n方",
		"cr":             "食\r方",
		"tab":            "食\t方",
		"nul":            "食\x00方",
		"del":            "食\x7f方",
		"c1":             "食\u0085方",
		"bidi override":  "食‮方",
		"zero width":     "食​方",
		"line separator": "食 方",
		"para separator": "食 方",
		"invalid utf8":   "食\xff方",
	} {
		if HeaderText(bad) {
			t.Errorf("%s: HeaderText(%q) = true", name, bad)
		}
	}
}

func TestBodyText(t *testing.T) {
	for _, ok := range []string{"验证码：{code}", "第一行\n\n\t第二行\n", " 前后空白可以 "} {
		if !BodyText(ok) {
			t.Errorf("BodyText(%q) = false", ok)
		}
	}
	for name, bad := range map[string]string{
		"empty":          "",
		"blank":          " \n\t ",
		"cr":             "a\r\nb",
		"nul":            "a\x00b",
		"esc":            "a\x1bb",
		"del":            "a\x7fb",
		"bidi override":  "a‮b",
		"zero width":     "a​b",
		"line separator": "a b",
		"para separator": "a b",
		"invalid utf8":   "a\xffb",
	} {
		if BodyText(bad) {
			t.Errorf("%s: BodyText(%q) = true", name, bad)
		}
	}
}

func TestRuneCountIsCodePoints(t *testing.T) {
	if !HeaderText(strings.Repeat("食", 1000)) {
		t.Fatal("HeaderText must not impose a length; callers apply their own limits")
	}
}
