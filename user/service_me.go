package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
)

const maxDisplayNameRunes = 128
const maxProviderNameRunes = 256

// Me 是当前用户资料（C 端 DTO 的数据源；不含任何身份或密钥字段）。
type Me struct {
	ID          string
	State       enum.UserState
	DisplayName string
	AvatarURL   string
	CreateTime  time.Time
	DeleteTime  *time.Time
	PurgeTime   *time.Time
}

func meFrom(u db.UserAccount) Me {
	m := Me{ID: u.ID, State: u.State, CreateTime: u.CreateTime, DeleteTime: u.DeleteTime, PurgeTime: u.PurgeTime}
	if u.AvatarUrl != nil {
		m.AvatarURL = *u.AvatarUrl
	}
	if u.DisplayName != nil {
		m.DisplayName = *u.DisplayName
	}
	return m
}

// GetMe 读取用户资料。
func (s *Service) GetMe(ctx context.Context, userID string) (Me, error) {
	u, err := s.d.Repo.Q().GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Me{}, ErrNotFound
	}
	if err != nil {
		return Me{}, fmt.Errorf("user: get me: %w", err)
	}
	return meFrom(u), nil
}

// isForbiddenDisplayNameRune 报告 r 是否为双向文本覆盖符或其他隐藏格式控制符。
//
// 只拒绝这些明确列出的 Cf 字符，而不是整个 Cf 类目：ZWJ（U+200D）与 ZWNJ（U+200C）
// 也属于 Cf，但分别是复合 emoji（如家庭、职业类序列）和波斯语/阿拉伯语及部分印度语文字
// 的正常连字控制符，会被真实用户合法输入，必须放行。这里列出的才是能让显示名在客户端
// 渲染时冒充别的文本的危险字符：
//   - U+202A–U+202E：LRE/RLE/PDF/LRO/RLO（双向文本嵌入/覆盖）
//   - U+2066–U+2069：LRI/RLI/FSI/PDI（双向文本隔离）
//   - U+200E、U+200F：LRM/RLM（方向标记，本身不隐藏字符，但与上述同源，一并列入白名单外）
//   - U+061C：ALM（阿拉伯字母标记）
//   - U+FEFF：BOM / ZWNBSP
func isForbiddenDisplayNameRune(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0x200E || r == 0x200F:
		return true
	case r == 0x061C:
		return true
	case r == 0xFEFF:
		return true
	default:
		return false
	}
}

func normalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	// 拒绝 Cc（真正的控制字符，如换行）以及一个明确列出的双向/格式控制符名单
	// （见 isForbiddenDisplayNameRune）——两者都能让显示名在客户端渲染时冒充别的文本，
	// 而 utf8.RuneCountInString 的长度检查看不出这类问题。ZWJ/ZWNJ 等其余 Cf 字符
	// 是合法输入（复合 emoji、波斯语/阿拉伯语连字等），故意放行。
	for _, r := range name {
		if unicode.IsControl(r) || isForbiddenDisplayNameRune(r) {
			return "", fmt.Errorf("%w: display_name must not contain control characters", ErrInvalidArgument)
		}
	}
	if utf8.RuneCountInString(name) > maxDisplayNameRunes {
		return "", fmt.Errorf("%w: display_name longer than %d characters", ErrInvalidArgument, maxDisplayNameRunes)
	}
	return name, nil
}

// sanitizeProviderName 去掉控制字符和双向格式符，再截到身份附加数据允许的长度。
// 空结果表示该声明不可用，调用方跳过字段，不因此拒绝登录。
func sanitizeProviderName(name string) string {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if unicode.IsControl(r) || isForbiddenDisplayNameRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	runes := []rune(out)
	if len(runes) > maxProviderNameRunes {
		out = strings.TrimSpace(string(runes[:maxProviderNameRunes]))
	}
	return out
}

// displayNameFromProvider 取已净化名称的前 128 个码位，供账号显示名首次填充。
func displayNameFromProvider(name string) string {
	name = sanitizeProviderName(name)
	runes := []rune(name)
	if len(runes) > maxDisplayNameRunes {
		name = strings.TrimSpace(string(runes[:maxDisplayNameRunes]))
	}
	return name
}

// UpdateDisplayName 修改显示名：TrimSpace，rune 数 ≤ 128，空串清空。
func (s *Service) UpdateDisplayName(ctx context.Context, userID, name string) (Me, error) {
	name, err := normalizeDisplayName(name)
	if err != nil {
		return Me{}, err
	}
	var p *string
	if name != "" {
		p = &name
	}
	u, err := s.d.Repo.Q().UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{ID: userID, DisplayName: p})
	if errors.Is(err, pgx.ErrNoRows) {
		return Me{}, ErrNotFound
	}
	if err != nil {
		return Me{}, fmt.Errorf("user: update display name: %w", err)
	}
	return meFrom(u), nil
}

// anchorOfUser 检查归一化后的 target 是否为该用户已绑定的锚点身份。
func (s *Service) anchorOfUser(ctx context.Context, q *db.Queries, userID string, kind enum.IdentityKind, norm string) (bool, error) {
	if !kind.IsAnchor() {
		return false, nil
	}
	idents, err := q.ListActiveIdentitiesByUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("user: list identities: %w", err)
	}
	digests := s.d.Digester.AllDigests(norm)
	for _, id := range idents {
		if id.Kind != kind || id.SubjectDigest == nil {
			continue
		}
		for _, d := range digests {
			if *id.SubjectDigest == d {
				return true, nil
			}
		}
	}
	return false, nil
}

// sendReauthenticationCode 向用户自己的锚点身份发送 REAUTH 码。
func (s *Service) sendReauthenticationCode(ctx context.Context, p Principal, channel enum.IdentityKind, target string, meta Meta) (CodeChallenge, error) {
	if !channel.IsChannel() {
		return CodeChallenge{}, ErrInvalidArgument
	}
	if err := s.requireIdentityEnabled(channel); err != nil {
		return CodeChallenge{}, err
	}
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return CodeChallenge{}, err
	}
	ok, err := s.anchorOfUser(ctx, s.d.Repo.Q(), p.UserID, channel, norm)
	if err != nil {
		return CodeChallenge{}, err
	}
	if !ok {
		return CodeChallenge{}, ErrNotAnchor
	}
	return s.sendCode(ctx, channel, enum.PurposeReauth, norm, code.Binding{UserID: p.UserID, SessionID: p.SessionID}, meta)
}

// reauthenticate 校验 REAUTH 码，刷新会话的 auth_time 并重签 access。
func (s *Service) reauthenticate(ctx context.Context, p Principal, cred CodeCredential, meta Meta) (TokenResult, error) {
	normalizedClientInfo, clientInfoErr := NormalizeClientInfo(meta.ClientInfo)
	if clientInfoErr != nil {
		return TokenResult{}, clientInfoErr
	}
	meta.ClientInfo = normalizedClientInfo
	if err := validateCodeCredential(cred); err != nil {
		return TokenResult{}, err
	}
	if err := s.requireIdentityEnabled(cred.Channel); err != nil {
		return TokenResult{}, err
	}
	channel, target := cred.Channel, cred.Target
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return TokenResult{}, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	ev := audit.AuthenticationEvent{UserID: p.UserID, SessionID: p.SessionID, IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, RequestID: meta.RequestID}

	if reason, err := s.verifyCode(ctx, channel, enum.PurposeReauth, norm, cred, code.Binding{UserID: p.UserID, SessionID: p.SessionID}); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			ev.Type, ev.Result, ev.Reason = enum.EventReauthenticationFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return TokenResult{}, err
	}

	ok, err := s.anchorOfUser(ctx, s.d.Repo.Q(), p.UserID, channel, norm)
	if err != nil {
		return TokenResult{}, err
	}
	if !ok {
		ev.Type, ev.Result, ev.Reason = enum.EventReauthenticationFailed, enum.ResultFailure, "NOT_ANCHOR"
		s.record(ctx, ev)
		return TokenResult{}, ErrNotAnchor
	}

	var res TokenResult
	err = s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		// 与身份写入一致，先锁用户再锁会话；锁后重新确认锚点。
		u, err := q.LockUserByID(ctx, p.UserID)
		if err != nil {
			return fmt.Errorf("user: load user: %w", err)
		}
		sess, err := q.LockSessionByID(ctx, p.SessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			s.observationReason(ctx, ob.ReasonSessionRevoked)
			return fmt.Errorf("%w: session not found", ErrInvalidToken)
		}
		if err != nil {
			return fmt.Errorf("user: lock session: %w", err)
		}
		if sess.UserID != p.UserID || sess.RevokeTime != nil {
			s.observationReason(ctx, ob.ReasonSessionRevoked)
			return fmt.Errorf("%w: session revoked", ErrInvalidToken)
		}
		if u.State == enum.UserFrozen {
			return ErrUserFrozen
		}
		if _, err := s.requireSessionAuthenticationIdentities(ctx, q, sess); err != nil {
			return err
		}
		ok, err := s.anchorOfUser(ctx, q, p.UserID, channel, norm)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotAnchor
		}
		complete, err := s.userIdentityComplete(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		// 等待 user/session 锁及身份复核后重新判期，不能用消费验证码前后的旧时间。
		now := sessionTime(s.now())
		if !sessionActiveAt(sess, now) {
			return errSessionExpired
		}
		identity, err := s.findProof(ctx, q, completionProof{Kind: channel, Target: norm})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotAnchor
		}
		if err != nil {
			return fmt.Errorf("user: find reauthentication identity: %w", err)
		}
		if identity.UserID != p.UserID {
			return ErrNotAnchor
		}
		if err := s.setSessionAuthenticationIdentities(ctx, q, sess, []string{identity.ID}); err != nil {
			return err
		}
		n, err := q.UpdateSessionAuthTime(ctx, db.UpdateSessionAuthTimeParams{ID: p.SessionID, AuthTime: now})
		if err != nil {
			return fmt.Errorf("user: update auth_time: %w", err)
		}
		if n == 0 {
			s.observationReason(ctx, ob.ReasonSessionRevoked)
			return fmt.Errorf("%w: session revoked", ErrInvalidToken)
		}
		if err := s.updateSessionClientInfo(ctx, q, sess, meta.ClientInfo, now); err != nil {
			return err
		}
		sess.AuthTime = now
		res, err = s.tokensFor(sess, "", deriveScope(u.State, complete), now)
		return err
	})
	if err != nil {
		// 只有能明确归因为业务拒绝的错误才记审计事件；其余（DB 故障等基础设施错误）是
		// 500 类错误，由调用方记录日志，这里不应把一次基础设施失败误记成"重新认证失败"的
		// 业务事件。
		switch {
		case errors.Is(err, ErrNotAnchor):
			ev.Type, ev.Result, ev.Reason = enum.EventReauthenticationFailed, enum.ResultFailure, "NOT_ANCHOR"
			s.record(ctx, ev)
		case errors.Is(err, errSessionExpired):
			ev.Type, ev.Result, ev.Reason = enum.EventReauthenticationFailed, enum.ResultFailure, "SESSION_EXPIRED"
			s.record(ctx, ev)
		case errors.Is(err, ErrInvalidToken):
			ev.Type, ev.Result, ev.Reason = enum.EventReauthenticationFailed, enum.ResultFailure, "SESSION_REVOKED"
			s.record(ctx, ev)
		case errors.Is(err, ErrUserFrozen):
			ev.Type, ev.Result, ev.Reason = enum.EventReauthenticationFailed, enum.ResultFailure, "USER_FROZEN"
			s.record(ctx, ev)
		}
		return TokenResult{}, err
	}
	ev.Type, ev.Result = enum.EventReauthenticated, enum.ResultSuccess
	s.record(ctx, ev)
	return res, nil
}
