package user

import (
	"crypto/sha256"
	"fmt"
	"github.com/bbxx111/accountkit/enum"
	"slices"
	"strings"
)

// IdentityPolicy 是装配时构造的不可变身份策略快照。
// Complete 接收已验证且未删除的身份类型，忽略不在 Enabled 中的历史身份。
type IdentityPolicy struct{ enabled, required []enum.IdentityKind }

// NewIdentityPolicy 中 nil 表示默认，非 nil 空切片表示显式空。
// defaultProviders 是已登记的第三方类型：Enabled 省略时全部加入；显式 Enabled 中的
// 非内置类型必须在其中。内置第三方类型是否具备验证能力由 ValidateCapabilities 判断。
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
			if !k.Valid() || seen[k] || !(k.IsBuiltin() || slices.Contains(defaultProviders, k)) {
				return fmt.Errorf("user: %s contains invalid, unregistered or duplicate kind %s", name, k)
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
	slices.SortFunc(p.enabled, compareIdentityKinds)
	slices.SortFunc(p.required, compareIdentityKinds)
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

// builtinKindOrder 保持内置类型的历史输出顺序；配置的类型排在其后并按名称排序。
var builtinKindOrder = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple, enum.IdentityGoogle, enum.IdentityFacebook}

func compareIdentityKinds(a, b enum.IdentityKind) int {
	ia, ib := slices.Index(builtinKindOrder, a), slices.Index(builtinKindOrder, b)
	switch {
	case ia >= 0 && ib >= 0:
		return ia - ib
	case ia >= 0:
		return -1
	case ib >= 0:
		return 1
	}
	return strings.Compare(string(a), string(b))
}

func (p *IdentityPolicy) EnabledKinds() []enum.IdentityKind  { return slices.Clone(p.enabled) }
func (p *IdentityPolicy) RequiredKinds() []enum.IdentityKind { return slices.Clone(p.required) }

// Fingerprint 对规范化集合生成稳定指纹，不包含提供方秘密。
func (p *IdentityPolicy) Fingerprint() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("identity-policy-v1:%v:%v", p.enabled, p.required))))
}

// IdentityCapabilities 描述装配能力，不表示依赖瞬时健康状态。Providers 为已登记的第三方类型。
type IdentityCapabilities struct {
	Phone, Email bool
	Providers    []enum.IdentityKind
}

// ValidateCapabilities 仅供显式 Enabled 使用；默认装配保留 Disabled 发送器兼容性。
func (p *IdentityPolicy) ValidateCapabilities(c IdentityCapabilities) error {
	for _, k := range p.enabled {
		capable := slices.Contains(c.Providers, k)
		switch k {
		case enum.IdentityPhone:
			capable = c.Phone
		case enum.IdentityEmail:
			capable = c.Email
		}
		if !capable {
			return fmt.Errorf("user: EnabledIdentityKinds %s has no verification capability", k)
		}
	}
	return nil
}
