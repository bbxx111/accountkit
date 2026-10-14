package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
)

// SendSignInCode 发送登录验证码（purpose SIGN_IN）。额度先占后发，发送失败不退还。
func (s *Service) SendSignInCode(ctx context.Context, channel enum.IdentityKind, target string, meta Meta) (CodeChallenge, error) {
	return s.sendCode(ctx, channel, enum.PurposeSignIn, target, code.Binding{}, meta)
}

// sendCode 是三种用途共用的发码流程；binding 用于轮次主体绑定和审计，登录时为空。
func (s *Service) sendCode(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target string, binding code.Binding, meta Meta) (CodeChallenge, error) {
	return s.sendCodeUntil(ctx, channel, purpose, target, binding, meta, time.Time{})
}

func (s *Service) sendCodeUntil(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target string, binding code.Binding, meta Meta, deadline time.Time) (CodeChallenge, error) {
	if err := s.requireIdentityEnabled(channel); err != nil {
		return CodeChallenge{}, err
	}
	if meta.IP == "" {
		// 空 IP 会让 code.Store 的 IP 维度限流把所有调用方并入同一个配额桶（quota:*:ip::<day>），
		// 一个客户端耗尽额度就会连带拒绝其余所有人；在触碰验证码存储之前先拒绝。
		s.d.Logger.Error("user: send code rejected: missing client ip", "channel", channel.String(), "purpose", purpose.String())
		return CodeChallenge{}, fmt.Errorf("%w: client ip is required", ErrInvalidArgument)
	}
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return CodeChallenge{}, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	base := audit.Event{UserID: binding.UserID, IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, RequestID: meta.RequestID}

	// 可选可用性契约保持旧宿主兼容：只实现原投递方法的发送器默认启用。
	var delivery any = s.d.SMS
	if channel == enum.IdentityEmail {
		delivery = s.d.Email
	}
	if capability, ok := delivery.(interface{ Enabled() bool }); ok && !capability.Enabled() {
		base.Type, base.Result, base.Reason = enum.EventCodeSendRejected, enum.ResultFailure, "CHANNEL_NOT_ENABLED"
		s.record(ctx, base)
		return CodeChallenge{}, sender.ErrDisabled
	}

	issued, err := s.d.Codes.IssueChallengeUntil(ctx, channel, purpose, norm, meta.IP, binding, deadline)
	if err != nil {
		var rl *code.RateLimitedError
		switch {
		case errors.As(err, &rl):
			base.Type, base.Result, base.Reason = enum.EventCodeSendRejected, enum.ResultFailure, rl.Dimension
			s.record(ctx, base)
			return CodeChallenge{}, err
		case errors.Is(err, code.ErrUnavailable):
			return CodeChallenge{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		default:
			return CodeChallenge{}, err
		}
	}
	msg := sender.Message{Purpose: purpose, Code: issued.Code, TTL: s.d.CodeTTL}
	if !deadline.IsZero() {
		msg.TTL = issued.ExpireTime.Sub(s.now())
	}
	switch channel {
	case enum.IdentityPhone:
		err = s.d.SMS.SendSMS(ctx, norm, msg)
	case enum.IdentityEmail:
		err = s.d.Email.SendEmail(ctx, norm, msg)
	}
	if err != nil {
		_ = s.d.Codes.DiscardChallenge(ctx, channel, purpose, norm, issued.CodeID)
		base.Type, base.Result, base.Reason = enum.EventCodeSendFailed, enum.ResultFailure, "SEND_FAILED"
		s.record(ctx, base)
		if errors.Is(err, sender.ErrUnavailable) {
			return CodeChallenge{}, ErrUnavailable // 不传播投递依赖的响应或收件地址；额度不退还。
		}
		return CodeChallenge{}, &deliveryError{cause: err} // 额度不退还，防止失败重试形成发送风暴
	}
	base.Type, base.Result, base.Reason = enum.EventCodeSent, enum.ResultSuccess, purpose.String()
	s.record(ctx, base)
	return CodeChallenge{CodeID: issued.CodeID, ExpireTime: issued.ExpireTime}, nil
}

// verifyCode 校验验证码并把 code 包的错误映射为审计原因。
func (s *Service) verifyCode(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, norm string, cred CodeCredential, binding code.Binding) (reason string, err error) {
	if err := s.requireIdentityEnabled(channel); err != nil {
		return "IDENTITY_KIND_NOT_ENABLED", err
	}
	err = s.d.Codes.VerifyChallenge(ctx, code.Credential{Channel: channel, Purpose: purpose, Target: norm, CodeID: cred.CodeID, Code: cred.Code, Binding: binding})
	return codeFailure(err)
}

func codeFailure(err error) (string, error) {
	var rl *code.RateLimitedError
	switch {
	case errors.As(err, &rl):
		return rl.Dimension, err
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

// SignInWithCode 验证码登录；未知身份按策略直接注册或返回待补全结果。
func (s *Service) SignInWithCode(ctx context.Context, cred CodeCredential, dev Device, meta Meta) (SignInResult, error) {
	if err := validateCodeCredential(cred); err != nil {
		return SignInResult{}, err
	}
	if err := s.requireIdentityEnabled(cred.Channel); err != nil {
		return SignInResult{}, err
	}
	channel, target := cred.Channel, cred.Target
	if err := validDevice(dev); err != nil {
		return SignInResult{}, err
	}
	norm, _, _, err := s.normalizeTarget(channel, target)
	if err != nil {
		return SignInResult{}, err
	}
	digest, _ := s.d.Digester.Digest(norm)
	ev := audit.Event{IdentityKind: channel, SubjectHint: audit.Hint(digest), IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID}

	expiry, verifyErr := s.d.Codes.VerifyChallengeWithExpiry(ctx, code.Credential{Channel: channel, Purpose: enum.PurposeSignIn, Target: norm, CodeID: cred.CodeID, Code: cred.Code})
	if reason, err := codeFailure(verifyErr); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			ev.Type, ev.Result, ev.Reason = enum.EventSignInFailed, enum.ResultFailure, reason
			s.record(ctx, ev)
		}
		return SignInResult{}, err
	}
	return s.signInVerified(ctx, completionProof{Kind: channel, Target: norm}, expiry, dev, meta)
}

// signInRejectReason 把旧证明及账号状态导致的登录拒绝映射为审计 reason；DB 故障等返回 ""，
// 调用方不为其写业务审计事件。
func signInRejectReason(err error) string {
	switch {
	case errors.Is(err, code.ErrInvalid):
		return "CODE_INVALID"
	case errors.Is(err, idp.ErrInvalidCredential):
		return "IDP_CREDENTIAL_INVALID"
	case errors.Is(err, ErrUserFrozen):
		return "USER_FROZEN"
	case errors.Is(err, ErrUserPendingDeletion):
		return "USER_PENDING_DELETION"
	default:
		return ""
	}
}

// deliveryError 使用固定错误文本，防止提供方私密消息进入日志；Unwrap 保留 errors.Is/As 识别。
type deliveryError struct{ cause error }

func (e *deliveryError) Error() string { return "user: verification delivery failed" }
func (e *deliveryError) Unwrap() error { return e.cause }
