package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/sender"
)

// SendSignInCode 发送登录验证码（purpose SIGN_IN）。额度先占后发，发送失败不退还。
func (s *Service) SendSignInCode(ctx context.Context, channel enum.IdentityKind, target string, meta Meta) error {
	return s.sendCode(ctx, channel, enum.PurposeSignIn, target, "", meta)
}

// sendCode 是三种用途共用的发码流程；userID 仅用于审计（未登录时为空）。
func (s *Service) sendCode(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, userID string, meta Meta) error {
	if meta.IP == "" {
		// 空 IP 会让 code.Store 的 IP 维度限流把所有调用方并入同一个配额桶（quota:*:ip::<day>），
		// 一个客户端耗尽额度就会连带拒绝其余所有人；在触碰验证码存储之前先拒绝。
		s.d.Logger.Error("user: send code rejected: missing client ip", "channel", channel.String(), "purpose", purpose.String())
		return fmt.Errorf("%w: client ip is required", ErrInvalidArgument)
	}
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return err
	}
	digest, _ := s.d.Digester.Digest(norm)
	base := audit.Event{UserID: userID, IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, RequestID: meta.RequestID}

	// 可选可用性契约保持旧宿主兼容：只实现原投递方法的发送器默认启用。
	var delivery any = s.d.SMS
	if channel == enum.IdentityEmail {
		delivery = s.d.Email
	}
	if capability, ok := delivery.(interface{ Enabled() bool }); ok && !capability.Enabled() {
		base.Type, base.Result, base.Reason = enum.EventCodeSendRejected, enum.ResultFailure, "CHANNEL_NOT_ENABLED"
		s.record(ctx, base)
		return sender.ErrDisabled
	}

	plain, err := s.d.Codes.Issue(ctx, channel, purpose, norm, meta.IP)
	if err != nil {
		var rl *code.RateLimitedError
		switch {
		case errors.As(err, &rl):
			base.Type, base.Result, base.Reason = enum.EventCodeSendRejected, enum.ResultFailure, rl.Dimension
			s.record(ctx, base)
			return err
		case errors.Is(err, code.ErrUnavailable):
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		default:
			return err
		}
	}
	msg := sender.Message{Purpose: purpose, Code: plain, TTL: s.d.CodeTTL}
	switch channel {
	case enum.IdentityPhone:
		err = s.d.SMS.SendSMS(ctx, norm, msg)
	case enum.IdentityEmail:
		err = s.d.Email.SendEmail(ctx, norm, msg)
	}
	if err != nil {
		base.Type, base.Result, base.Reason = enum.EventCodeSendFailed, enum.ResultFailure, "SEND_FAILED"
		s.record(ctx, base)
		if errors.Is(err, sender.ErrUnavailable) {
			return ErrUnavailable // 不传播投递依赖的响应或收件地址；额度不退还。
		}
		return fmt.Errorf("user: send code: %w", err) // 额度不退还，防止失败重试形成发送风暴
	}
	base.Type, base.Result, base.Reason = enum.EventCodeSent, enum.ResultSuccess, purpose.String()
	s.record(ctx, base)
	return nil
}

// verifyCode 校验验证码并把 code 包的错误映射为审计原因。
func (s *Service) verifyCode(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, norm, plain string) (reason string, err error) {
	err = s.d.Codes.Verify(ctx, channel, purpose, norm, plain)
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, code.ErrInvalid):
		return "CODE_INVALID", err
	case errors.Is(err, code.ErrExpired):
		return "CODE_EXPIRED", err
	case errors.Is(err, code.ErrExhausted):
		return "CODE_ATTEMPTS_EXHAUSTED", err
	case errors.Is(err, code.ErrUnavailable):
		return "UNAVAILABLE", fmt.Errorf("%w: %v", ErrUnavailable, err)
	default:
		return "INTERNAL", err
	}
}

// SignInWithCode 验证码登录；subject 不存在则同一事务内创建账号与身份（登录即注册）。
func (s *Service) SignInWithCode(ctx context.Context, channel enum.IdentityKind, target, plainCode string, dev Device, meta Meta) (TokenResult, error) {
	if err := validDevice(dev); err != nil {
		return TokenResult{}, err
	}
	norm, hintPrefix, hintSuffix, err := s.normalizeTarget(channel, target)
	if err != nil {
		return TokenResult{}, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	ev := audit.Event{IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID}

	if reason, err := s.verifyCode(ctx, channel, enum.PurposeSignIn, norm, plainCode); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return TokenResult{}, err
	}

	now := s.now()
	var res TokenResult
	var replaced []string
	err = s.withRetryOnUnique(ctx, func(q *db.Queries) error {
		replaced = replaced[:0]
		u, isNew, err := s.findOrCreateUser(ctx, q, channel, norm, hintPrefix, hintSuffix)
		if err != nil {
			return err
		}
		ev.UserID = u.ID
		sess, r, rep, err := s.establishSession(ctx, q, u, channel.IsAnchor(), dev, now)
		if err != nil {
			return err
		}
		res, replaced = r, rep
		res.IsNewUser = isNew
		ev.UserID, ev.SessionID = u.ID, sess.ID
		return nil
	})
	if err != nil {
		if reason := signInRejectReason(err); reason != "" {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return TokenResult{}, err
	}
	s.finishSignIn(ctx, ev, res, replaced, dev, meta)
	return res, nil
}

// findOrCreateUser 按 (kind, 多版本 digest) 查身份；不存在则创建账号 + 身份。
func (s *Service) findOrCreateUser(ctx context.Context, q *db.Queries, kind enum.IdentityKind, norm, hintPrefix, hintSuffix string) (db.UserAccount, bool, error) {
	ident, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: kind, Digests: s.d.Digester.AllDigests(norm)})
	switch {
	case err == nil:
		// 加行锁而非普通读：同一用户、同一设备的两次并发登录都会走到这里，锁把它们串行化，
		// 第二个事务要等第一个提交（吊销旧会话 + 建新会话）之后才能继续，从而不会与刚吊销的
		// 会话一起并存出两条活跃会话。新用户路径已经被下面的 CreateUser 唯一约束天然串行化，
		// 无需额外加锁。
		u, err := q.LockUserByID(ctx, ident.UserID)
		if err != nil {
			return db.UserAccount{}, false, fmt.Errorf("user: lock user %s: %w", ident.UserID, err)
		}
		// 身份可能在等待用户锁期间被解绑或替换；旧证明不能登录原账号。
		current, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{Kind: kind, Digests: s.d.Digester.AllDigests(norm)})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.UserAccount{}, false, code.ErrInvalid
		}
		if err != nil {
			return db.UserAccount{}, false, fmt.Errorf("user: recheck identity: %w", err)
		}
		if current.ID != ident.ID || current.UserID != u.ID {
			return db.UserAccount{}, false, code.ErrInvalid
		}
		return u, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return db.UserAccount{}, false, fmt.Errorf("user: find identity: %w", err)
	}
	uid, err := ids.New(ids.User)
	if err != nil {
		return db.UserAccount{}, false, err
	}
	u, err := q.CreateUser(ctx, db.CreateUserParams{ID: uid, State: enum.UserActive})
	if err != nil {
		return db.UserAccount{}, false, err // 唯一冲突由 withRetryOnUnique 重试
	}
	if _, err := s.createAnchorIdentity(ctx, q, uid, kind, norm, hintPrefix, hintSuffix); err != nil {
		return db.UserAccount{}, false, err // 并发首次登录同一 subject → 唯一冲突 → 重试后走"已存在"分支
	}
	return u, true, nil
}

// signInRejectReason 把旧证明及账号状态导致的登录拒绝映射为审计 reason；DB 故障等返回 ""，
// 调用方不为其写业务审计事件。
func signInRejectReason(err error) string {
	switch {
	case errors.Is(err, code.ErrInvalid):
		return "CODE_INVALID"
	case errors.Is(err, ErrUserFrozen):
		return "USER_FROZEN"
	case errors.Is(err, ErrUserPendingDeletion):
		return "USER_PENDING_DELETION"
	default:
		return ""
	}
}
