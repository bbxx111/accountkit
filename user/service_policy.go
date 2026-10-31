package user

import (
	"context"
	"fmt"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/bbxx111/accountkit/user/idp"
)

// requireIdentityEnabled 在消费凭证或调用外部提供方之前执行部署策略检查。
func (s *Service) requireIdentityEnabled(kind enum.IdentityKind) error {
	if !kind.Valid() {
		return fmt.Errorf("%w: invalid identity kind", ErrInvalidArgument)
	}
	if s.IdentityPolicy().Allows(kind) {
		return nil
	}
	// 省略 Enabled 的宿主保留未装配提供方的原错误契约。
	if s.d.EnabledIdentityKinds == nil && !kind.IsAnchor() && s.providers[kind] == nil {
		return fmt.Errorf("%w: identity provider not configured", idp.ErrAppNotAllowed)
	}
	return ErrIdentityKindNotEnabled
}

func (s *Service) identitiesComplete(idents []db.Identity) bool {
	kinds := make([]enum.IdentityKind, 0, len(idents))
	for _, ident := range idents {
		kinds = append(kinds, ident.Kind)
	}
	return s.IdentityPolicy().Complete(kinds)
}

// userIdentityComplete 仅使用已验证且未软删除的身份，禁用历史身份不计入完整性。
func (s *Service) userIdentityComplete(ctx context.Context, q *db.Queries, userID string) (bool, error) {
	idents, err := q.ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("user: list identities for policy: %w", err)
	}
	return s.identitiesComplete(idents), nil
}

// newProviderRegistry 复制并校验已登记的第三方提供方，返回不可变索引与类型列表。
func newProviderRegistry(list []idp.Provider) (map[enum.IdentityKind]idp.Provider, []enum.IdentityKind, error) {
	registry := make(map[enum.IdentityKind]idp.Provider, len(list))
	kinds := make([]enum.IdentityKind, 0, len(list))
	for i, p := range list {
		if p == nil {
			return nil, nil, fmt.Errorf("user: Deps.Providers[%d] is nil", i)
		}
		kind := p.Kind()
		if !kind.Valid() || kind.IsAnchor() {
			return nil, nil, fmt.Errorf("user: Deps.Providers[%d] has invalid kind %q", i, string(kind))
		}
		if _, dup := registry[kind]; dup {
			return nil, nil, fmt.Errorf("user: Deps.Providers: duplicate kind %s", kind)
		}
		registry[kind] = p
		kinds = append(kinds, kind)
	}
	return registry, kinds, nil
}
