package user

import (
	"crypto/sha256"
	"fmt"
	"github.com/bbxx111/accountkit/enum"
	"slices"
)

// IdentityPolicy 是装配时构造的不可变身份策略快照。
// Complete 接收已验证且未删除的身份类型，忽略不在 Enabled 中的历史身份。
type IdentityPolicy struct{ enabled, required []enum.IdentityKind }

// NewIdentityPolicy 中 nil 表示默认，非 nil 空切片表示显式空。
// defaultProviders 仅用于 Enabled 省略时加入已经配置的第三方提供方。
func NewIdentityPolicy(enabled, required []enum.IdentityKind, defaultProviders ...enum.IdentityKind) (*IdentityPolicy, error) {
	if enabled == nil {
		enabled = append([]enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}, defaultProviders...)
	}
	if required == nil {
		required = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}
	}
	if len(enabled) == 0 {
		return nil, fmt.Errorf("user: EnabledIdentityKinds must not be empty")
	}
	check := func(name string, kinds []enum.IdentityKind) error {
		seen := map[enum.IdentityKind]bool{}
		for _, k := range kinds {
			if !k.Valid() || seen[k] {
				return fmt.Errorf("user: %s contains invalid or duplicate kind %s", name, k)
			}
			seen[k] = true
		}
		return nil
	}
	if err := check("EnabledIdentityKinds", enabled); err != nil {
		return nil, err
	}
	if err := check("RequiredIdentityKinds", required); err != nil {
		return nil, err
	}
	for _, k := range required {
		if !k.IsChannel() || !slices.Contains(enabled, k) {
			return nil, fmt.Errorf("user: RequiredIdentityKinds must contain only enabled PHONE/EMAIL kinds")
		}
	}
	p := &IdentityPolicy{enabled: slices.Clone(enabled), required: slices.Clone(required)}
	slices.Sort(p.enabled)
	slices.Sort(p.required)
	return p, nil
}
func (p *IdentityPolicy) Allows(k enum.IdentityKind) bool { return slices.Contains(p.enabled, k) }
func (p *IdentityPolicy) Complete(kinds []enum.IdentityKind) bool {
	for _, k := range kinds {
		if p.Allows(k) && (len(p.required) == 0 || slices.Contains(p.required, k)) {
			return true
		}
	}
	return false
}
func (p *IdentityPolicy) EnabledKinds() []enum.IdentityKind  { return slices.Clone(p.enabled) }
func (p *IdentityPolicy) RequiredKinds() []enum.IdentityKind { return slices.Clone(p.required) }

// Fingerprint 对规范化集合生成稳定指纹，不包含提供方秘密。
func (p *IdentityPolicy) Fingerprint() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("identity-policy-v1:%v:%v", p.enabled, p.required))))
}

// IdentityCapabilities 描述装配能力，不表示依赖瞬时健康状态。
type IdentityCapabilities struct{ Phone, Email, WeChat, Apple, Google bool }

// ValidateCapabilities 仅供显式 Enabled 使用；默认装配保留 Disabled 发送器兼容性。
func (p *IdentityPolicy) ValidateCapabilities(c IdentityCapabilities) error {
	for _, k := range p.enabled {
		if !map[enum.IdentityKind]bool{enum.IdentityPhone: c.Phone, enum.IdentityEmail: c.Email, enum.IdentityWeChat: c.WeChat, enum.IdentityApple: c.Apple, enum.IdentityGoogle: c.Google}[k] {
			return fmt.Errorf("user: EnabledIdentityKinds %s has no verification capability", k)
		}
	}
	return nil
}
