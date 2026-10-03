package phone_test

import (
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/phone"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ raw, region, want string }{
		{"13812341234", "CN", "+8613812341234"},
		{"+86 138 1234 1234", "CN", "+8613812341234"},
		{"+8613812341234", "US", "+8613812341234"}, // 带 + 时忽略默认区域
		{"(415) 555-2671", "US", "+14155552671"},
		{"020 7946 0958", "GB", "+442079460958"},
	}
	for _, c := range cases {
		got, err := phone.Normalize(c.raw, c.region)
		if err != nil || got != c.want {
			t.Errorf("Normalize(%q,%q) = %q, %v; want %q", c.raw, c.region, got, err, c.want)
		}
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"", "abc", "123", "+86 1", "13812341234x", "13812341234 ext 5", "+86 138-1234-1234#"} {
		if _, err := phone.Normalize(raw, "CN"); !errors.Is(err, phone.ErrInvalid) {
			t.Errorf("Normalize(%q): err = %v, want ErrInvalid", raw, err)
		}
	}
	if _, err := phone.Normalize("13812341234", ""); !errors.Is(err, phone.ErrInvalid) {
		t.Error("national number without a default region must be invalid")
	}
}

func TestHintsAndMask(t *testing.T) {
	cases := []struct{ e164, prefix, suffix, mask string }{
		{"+8613812341234", "+86138", "1234", "+86 138****1234"}, // NSN 11 位
		{"+14155552671", "+1415", "2671", "+1 415****2671"},     // NSN 10 位
		{"+442079460958", "+44207", "0958", "+44 207****0958"},  // NSN 10 位
		{"+4721234567", "+47", "4567", "+47 ****4567"},          // 挪威 NSN 8 位：仅国家码（法罗群岛号码在本版本 libphonenumber 元数据中判定无效，改用此号覆盖分支）
	}
	for _, c := range cases {
		p, s, err := phone.Hints(c.e164)
		if err != nil || p != c.prefix || s != c.suffix {
			t.Errorf("Hints(%q) = %q,%q,%v; want %q,%q", c.e164, p, s, err, c.prefix, c.suffix)
			continue
		}
		if got := phone.Mask(p, s); got != c.mask {
			t.Errorf("Mask(%q,%q) = %q, want %q", p, s, got, c.mask)
		}
	}
	if _, _, err := phone.Hints("13812341234"); !errors.Is(err, phone.ErrInvalid) {
		t.Error("Hints requires E.164 input")
	}
}
