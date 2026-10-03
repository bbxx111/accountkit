// Package phone 处理手机号：解析归一化为 E.164、拆分国家码与国内号码（NSN），
// 并按设计文档 §3.3 生成检索提示与掩码——提示里存的正是掩码展示可见的部分，
// 因此 hint 列不泄露管理员本来看不到的信息。
package phone

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// ErrInvalid 表示输入不是可解析的有效手机号。
var ErrInvalid = errors.New("phone: invalid phone number")

// allowedInput 限制 Normalize 接受的字符：数字、空格、括号、连字符、点，以及可选的前导 "+"。
// 登录标识必须是纯订户号码，字母（含分机号 "x"/"ext"）或其他符号一律拒绝。
var allowedInput = regexp.MustCompile(`^\+?[0-9 ()\-.]+$`)

// Normalize 把 raw 解析为 E.164。raw 以 "+" 开头时按国际格式解析并忽略 defaultRegion；
// 否则按 defaultRegion 解析。无效号码返回 ErrInvalid。不接受分机号（x/ext），
// 也不接受数字、空格、括号、连字符、点和前导 + 以外的字符。
func Normalize(raw, defaultRegion string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalid
	}
	if !allowedInput.MatchString(raw) {
		return "", fmt.Errorf("%w: contains characters other than digits, spaces, ()-. and a leading +", ErrInvalid)
	}
	if !strings.HasPrefix(raw, "+") && defaultRegion == "" {
		return "", fmt.Errorf("%w: national number without default region", ErrInvalid)
	}
	num, err := phonenumbers.Parse(raw, strings.ToUpper(defaultRegion))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if num.GetExtension() != "" {
		return "", fmt.Errorf("%w: extensions are not allowed", ErrInvalid)
	}
	if !phonenumbers.IsValidNumber(num) {
		return "", ErrInvalid
	}
	return phonenumbers.Format(num, phonenumbers.E164), nil
}

// Hints 计算检索提示。输入必须是 E.164（以 "+" 开头且有效）。
//   - prefix：国家码（带 +）加国内号码前 3 位；国内号码不足 9 位时只保留国家码，
//     保证中间至少遮 2 位。
//   - suffix：国内号码后 4 位。
func Hints(e164 string) (prefix, suffix string, err error) {
	if !strings.HasPrefix(e164, "+") {
		return "", "", fmt.Errorf("%w: Hints requires E.164 input", ErrInvalid)
	}
	num, err := phonenumbers.Parse(e164, "")
	if err != nil || !phonenumbers.IsValidNumber(num) {
		return "", "", ErrInvalid
	}
	cc := fmt.Sprintf("+%d", num.GetCountryCode())
	nsn := phonenumbers.GetNationalSignificantNumber(num)
	if len(nsn) < 4 {
		return "", "", ErrInvalid
	}
	prefix = cc
	if len(nsn) >= 9 {
		prefix += nsn[:3]
	}
	return prefix, nsn[len(nsn)-4:], nil
}

// Mask 由提示拼出掩码展示。prefix 形如 "+86138" 或 "+44"；suffix 为 4 位。
// 输出把国家码与国内前缀之间加一个空格："+86 138****1234"、"+44 ****1234"。
func Mask(prefix, suffix string) string {
	// 国家码 1–3 位数字：找到 "+" 后第一段与 NSN 前缀的分界。libphonenumber 国家码不含前导 0，
	// 而 NSN 前 3 位可能以 0 开头（如英国 020），因此不能靳靠数字特征切分——
	// 用 Hints 的构造规则反推：前缀总长 = 国家码长 + 0 或 3。
	digits := strings.TrimPrefix(prefix, "+")
	ccLen := len(digits)
	if ccLen > 3 {
		ccLen -= 3
	}
	cc, nat := digits[:ccLen], digits[ccLen:]
	return "+" + cc + " " + nat + "****" + suffix
}
