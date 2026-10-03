// Package ids 生成并校验对外资源 id：`<kind>_<TSID>`。
//
// TSID 部分为 13 字符小写 Crockford base32：41 bit 毫秒时间戳（纪元
// 2026-01-01T00:00:00Z）+ 22 bit 加密随机。字母表按 ASCII 单调，因此在
// COLLATE "C" 下字典序 ≡ 时间序，B-tree 局部性得以保留。类型前缀让日志可读，
// 并让服务端在 id 传错位置时直接 400。全局唯一性由主键唯一约束 + 调用方重试保证。
// id 是名字不是秘密：22 bit 随机不足以防猜测，验证码、refresh token 等凭证必须走各自的高熵随机生成。
package ids

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Kind 是资源类型前缀（不含下划线）。
type Kind string

const (
	User       Kind = "u"
	Identity   Kind = "i"
	Session    Kind = "s"
	AuditEvent Kind = "e"
)

// Alphabet 是小写 Crockford base32 字符集，剔除 i/l/o/u。
const Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// TSIDLength 是 TSID 部分的定长：⌈64/5⌉ = 13。
const TSIDLength = 13

var epochMillis = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

const (
	timestampBits = 41
	randomBits    = 22
)

// New 生成 "<kind>_<TSID>"。错误仅来自系统随机源。
func New(kind Kind) (string, error) {
	ms := uint64(time.Now().UnixMilli()-epochMillis) & (1<<timestampBits - 1)
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ids: random source: %w", err)
	}
	r := (uint64(b[0])<<16 | uint64(b[1])<<8 | uint64(b[2])) & (1<<randomBits - 1)
	return string(kind) + "_" + encode(ms<<randomBits|r), nil
}

// Valid 报告 s 是否为 kind 的合法 id。大写不合法：对外只发小写，输入不做宽容归一化。
func Valid(kind Kind, s string) bool {
	p := string(kind) + "_"
	if len(s) != len(p)+TSIDLength || s[:len(p)] != p {
		return false
	}
	for i := len(p); i < len(s); i++ {
		if !inAlphabet(s[i]) {
			return false
		}
	}
	return true
}

// Pattern 返回迁移 CHECK 约束使用的正则，与 Valid 是同一事实的两种写法。
func Pattern(kind Kind) string {
	return fmt.Sprintf(`^%s_[0-9a-hjkmnp-tv-z]{%d}$`, kind, TSIDLength)
}

func inAlphabet(c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'z':
		return c != 'i' && c != 'l' && c != 'o' && c != 'u'
	default:
		return false
	}
}

func encode(v uint64) string {
	var out [TSIDLength]byte
	for i := TSIDLength - 1; i >= 0; i-- {
		out[i] = Alphabet[v&31]
		v >>= 5
	}
	return string(out[:])
}
