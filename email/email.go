// Package email 归一化邮箱地址并生成检索提示与掩码（设计文档 §1.2、§3.3）。
// 规则刻意保守：只接受 ASCII、恰好一个 @、域名含点；国际化邮箱不在支持范围内。
package email

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ErrInvalid 表示输入不是可接受的邮箱地址。
var ErrInvalid = errors.New("email: invalid email address")

const maxLen = 254

// Normalize 返回小写、NFC、ASCII 的规范形式。
func Normalize(raw string) (string, error) {
	// Check for control characters in raw input before trimming
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: contains control character", ErrInvalid)
		}
	}

	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrInvalid
	}
	s = norm.NFC.String(s)
	for _, r := range s {
		if r > unicode.MaxASCII || unicode.IsSpace(r) {
			return "", fmt.Errorf("%w: non-ASCII or whitespace character", ErrInvalid)
		}
	}
	if len(s) > maxLen {
		return "", fmt.Errorf("%w: longer than %d", ErrInvalid, maxLen)
	}
	s = strings.ToLower(s)
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") || !strings.Contains(domain, ".") {
		return "", fmt.Errorf("%w: expected local@domain.tld", ErrInvalid)
	}
	if !validLocal(local) || !validDomain(domain) {
		return "", fmt.Errorf("%w: disallowed character", ErrInvalid)
	}
	return s, nil
}

// validLocal 允许 RFC 5322 dot-atom 的常见子集：字母数字与 . _ % + -（不允许引号形式）。
func validLocal(s string) bool {
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '%', r == '+', r == '-':
		default:
			return false
		}
	}
	return true
}

// validDomain 允许字母数字、连字符与点；每个标签非空且不以连字符开头/结尾。
func validDomain(s string) bool {
	for _, label := range strings.Split(s, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			default:
				return false
			}
		}
	}
	return true
}

// Hints 由已归一化的地址计算检索提示：本地部分前 2 字符（≤ 2 字符时 1 个）与域名。
func Hints(normalized string) (prefix, suffix string, err error) {
	local, domain, ok := strings.Cut(normalized, "@")
	if !ok || local == "" || domain == "" || normalized != strings.ToLower(normalized) {
		return "", "", ErrInvalid
	}
	n := 2
	if len(local) <= 2 {
		n = 1
	}
	return local[:n], domain, nil
}

// Mask 由提示拼出掩码展示。
func Mask(prefix, suffix string) string { return prefix + "***@" + suffix }
