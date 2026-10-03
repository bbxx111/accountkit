package pii

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Digester 计算带密钥的 HMAC-SHA256 摘要（64 位小写 hex），用于等值查找。
// 带密钥而非裸哈希：摘要列泄露后不能离线枚举手机号空间（~1.4e9）。
type Digester struct {
	keys     map[uint16][]byte
	active   uint16
	versions []uint16
}

// NewDigester 校验并构造；密钥被拷贝，调用方之后改自己的 slice 不影响摘要。
func NewDigester(keys map[uint16][]byte, active uint16) (*Digester, error) {
	copied, versions, err := checkKeys(keys, active)
	if err != nil {
		return nil, err
	}
	return &Digester{keys: copied, active: active, versions: versions}, nil
}

func digestWith(key []byte, plain string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(plain))
	return hex.EncodeToString(mac.Sum(nil))
}

// Digest 用 active 版本计算摘要。
func (d *Digester) Digest(plain string) (string, uint16) {
	return digestWith(d.keys[d.active], plain), d.active
}

// DigestFor 用指定版本计算摘要；版本未配置报错。
func (d *Digester) DigestFor(plain string, version uint16) (string, error) {
	k, ok := d.keys[version]
	if !ok {
		return "", fmt.Errorf("pii: no key configured for version %d", version)
	}
	return digestWith(k, plain), nil
}

// AllDigests 返回全部版本下的摘要：active 在前，其余升序。
// 查找谓词用 "digest IN (...)" 覆盖历史版本写入的行；漏掉任一活跃版本会让旧行不可达，
// 同一手机号就能被再次注册。
func (d *Digester) AllDigests(plain string) []string {
	out := make([]string, 0, len(d.versions))
	out = append(out, digestWith(d.keys[d.active], plain))
	for _, v := range d.versions {
		if v != d.active {
			out = append(out, digestWith(d.keys[v], plain))
		}
	}
	return out
}

// Active 返回当前签发用的版本。
func (d *Digester) Active() uint16 { return d.active }

// Versions 返回全部已配置版本（升序，拷贝）。
func (d *Digester) Versions() []uint16 { return append([]uint16(nil), d.versions...) }
