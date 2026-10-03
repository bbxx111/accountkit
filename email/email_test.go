package email_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/email"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  Bo.Bai@Shifang.CO ": "bo.bai@shifang.co",
		"A@B.CD":               "a@b.cd",
		"x+tag@example.com":    "x+tag@example.com",
	}
	for in, want := range cases {
		got, err := email.Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeRejects(t *testing.T) {
	bad := []string{
		"", "plain", "a@", "@b.cd", "a@b", "a@@b.cd", "a b@c.de", "a@c.de\n",
		"用户@example.com", "user@例子.com", // 非 ASCII
		strings.Repeat("a", 250) + "@b.cd", // > 254
		"a@b.cd;",                          // 分号不属于合法本地部分/域名字符
	}
	for _, in := range bad {
		if _, err := email.Normalize(in); !errors.Is(err, email.ErrInvalid) {
			t.Errorf("Normalize(%q): err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestHintsAndMask(t *testing.T) {
	cases := []struct{ in, prefix, suffix, mask string }{
		{"bo.bai@shifang.co", "bo", "shifang.co", "bo***@shifang.co"},
		{"x@example.com", "x", "example.com", "x***@example.com"},
		{"ab@example.com", "a", "example.com", "a***@example.com"},
		{"abc@example.com", "ab", "example.com", "ab***@example.com"},
	}
	for _, c := range cases {
		p, s, err := email.Hints(c.in)
		if err != nil || p != c.prefix || s != c.suffix {
			t.Errorf("Hints(%q) = %q,%q,%v; want %q,%q", c.in, p, s, err, c.prefix, c.suffix)
			continue
		}
		if got := email.Mask(p, s); got != c.mask {
			t.Errorf("Mask = %q, want %q", got, c.mask)
		}
	}
	if _, _, err := email.Hints("not-an-email"); !errors.Is(err, email.ErrInvalid) {
		t.Error("Hints must reject invalid input")
	}
}
