package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/bbxx111/accountkit/enum"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// operationFacts belong to one synchronous public invocation. A nested public
// invocation replaces the private context value, so its outcome cannot leak out.
// These facts distinguish branches whose public errors deliberately remain equal.
type operationFacts struct {
	reason ob.Reason
	mode   ob.RefreshMode
}
type operationFactsKey struct{ service *Service }

func (s *Service) observationContext(ctx context.Context) (context.Context, *operationFacts) {
	f := &operationFacts{}
	return context.WithValue(ctx, operationFactsKey{service: s}, f), f
}

func (s *Service) observationReason(ctx context.Context, reason ob.Reason) {
	if f, ok := ctx.Value(operationFactsKey{service: s}).(*operationFacts); ok {
		f.reason = reason
	}
}

func (s *Service) observationMode(ctx context.Context, mode ob.RefreshMode) {
	if f, ok := ctx.Value(operationFactsKey{service: s}).(*operationFacts); ok {
		f.mode = mode
	}
}

func observationMethod(kind enum.IdentityKind) ob.Method {
	switch kind {
	case enum.IdentityPhone:
		return ob.MethodPhone
	case enum.IdentityEmail:
		return ob.MethodEmail
	case enum.IdentityWeChat:
		return ob.MethodWechat
	case enum.IdentityApple:
		return ob.MethodApple
	case enum.IdentityGoogle:
		return ob.MethodGoogle
	case enum.IdentityFacebook:
		return ob.MethodFacebook
	case enum.IdentityAlipay:
		return ob.MethodAlipay
	case enum.IdentityQQ:
		return ob.MethodQQ
	case enum.IdentityWeibo:
		return ob.MethodWeibo
	case enum.IdentityKindUnspecified:
		return ob.MethodUnknown
	default:
		return ob.MethodProvider
	}
}

func observationChannel(kind enum.IdentityKind) ob.Channel {
	switch kind {
	case enum.IdentityPhone:
		return ob.ChannelPhone
	case enum.IdentityEmail:
		return ob.ChannelEmail
	default:
		return ob.ChannelUnknown
	}
}

func observationPurpose(purpose enum.CodePurpose) ob.Purpose {
	switch purpose {
	case enum.PurposeSignIn:
		return ob.PurposeSignIn
	case enum.PurposeBind:
		return ob.PurposeBind
	case enum.PurposeReauth:
		return ob.PurposeReauth
	case enum.PurposeSignInComplete:
		return ob.PurposeSignInComplete
	default:
		return ob.PurposeUnknown
	}
}

func observationGrant(scope string) ob.Grant {
	switch scope {
	case ScopeUser:
		return ob.GrantUser
	case ScopeBind:
		return ob.GrantBind
	case ScopeUndelete:
		return ob.GrantUndelete
	default:
		return ob.GrantUnknown
	}
}

// classifyOperation uses returned causes, never ctx.Err at recording time: a
// successfully committed call remains successful if its context is canceled later.
func classifyOperation(err error) (ob.Result, ob.Reason) {
	if err == nil {
		return ob.ResultSuccess, ob.ReasonNone
	}
	switch {
	case errors.Is(err, context.Canceled):
		return ob.ResultCanceled, ob.ReasonCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ob.ResultDeadlineExceeded, ob.ReasonDeadlineExceeded
	case errors.Is(err, ErrIdentityKindNotEnabled):
		return ob.ResultRejected, ob.ReasonIdentityKindDisabled
	case errors.Is(err, sender.ErrDisabled):
		return ob.ResultRejected, ob.ReasonChannelDisabled
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, ErrInvalidTarget), errors.Is(err, sender.ErrTargetNotSupported), errors.Is(err, ErrInvalidClientInfo), errors.Is(err, idp.ErrMalformedCredential):
		return ob.ResultRejected, ob.ReasonInvalidArgument
	case errors.Is(err, code.ErrInvalid):
		return ob.ResultRejected, ob.ReasonCodeInvalid
	case errors.Is(err, code.ErrExpired):
		return ob.ResultRejected, ob.ReasonCodeExpired
	case errors.Is(err, idp.ErrInvalidCredential), errors.Is(err, idp.ErrNonceReplayed), errors.Is(err, idp.ErrAppNotAllowed):
		return ob.ResultRejected, ob.ReasonInvalidCredential
	case errors.Is(err, ErrUserFrozen):
		return ob.ResultRejected, ob.ReasonUserFrozen
	case errors.Is(err, ErrUserPendingDeletion):
		return ob.ResultRejected, ob.ReasonUserPendingDeletion
	case errors.Is(err, ErrInvalidState):
		return ob.ResultRejected, ob.ReasonInvalidAccountState
	case errors.Is(err, ErrIdentityConflict):
		return ob.ResultRejected, ob.ReasonIdentityConflict
	case errors.Is(err, ErrNotAnchor), errors.Is(err, ErrLastAnchor), errors.Is(err, ErrReauthenticationRequired), errors.Is(err, ErrInsufficientScope):
		return ob.ResultRejected, ob.ReasonIdentityRequired
	case errors.Is(err, ErrIdentityKindLimit):
		return ob.ResultRejected, ob.ReasonIdentityLimit
	case errors.Is(err, ErrSignInCompletionInvalid):
		return ob.ResultRejected, ob.ReasonFlowInvalid
	case errors.Is(err, ErrSignInAlreadyCompleted):
		return ob.ResultRejected, ob.ReasonFlowConflict
	case errors.Is(err, errSessionExpired):
		return ob.ResultRejected, ob.ReasonSessionExpired
	case errors.Is(err, ErrInvalidToken):
		return ob.ResultRejected, ob.ReasonInvalidCredential
	case errors.Is(err, ErrInvalidGrant):
		return ob.ResultRejected, ob.ReasonUnknownToken
	case errors.Is(err, ErrUnavailable), errors.Is(err, code.ErrUnavailable), errors.Is(err, idp.ErrUnavailable), errors.Is(err, sender.ErrUnavailable), errors.Is(err, grace.ErrUnavailable), errors.Is(err, revocation.ErrUnavailable):
		return ob.ResultDependencyError, ob.ReasonDependencyUnavailable
	}
	var limited *code.RateLimitedError
	if errors.As(err, &limited) {
		switch limited.Dimension {
		case "COOLDOWN":
			return ob.ResultRejected, ob.ReasonCooldown
		case "TARGET_LIMIT":
			return ob.ResultRejected, ob.ReasonTargetLimit
		case "IP_LIMIT":
			return ob.ResultRejected, ob.ReasonIPLimit
		case "TARGET_VERIFY_LIMIT":
			return ob.ResultRejected, ob.ReasonTargetVerifyLimit
		default:
			return ob.ResultRejected, ob.ReasonUnknown
		}
	}
	var delivery *deliveryError
	if errors.As(err, &delivery) {
		return ob.ResultDependencyError, ob.ReasonSendFailed
	}
	// Database and transport errors are recognized by type, not message text.
	var database *pgconn.PgError
	var connect *pgconn.ConnectError
	var network net.Error
	if errors.As(err, &database) || errors.As(err, &connect) || errors.As(err, &network) || errors.Is(err, pgx.ErrTxCommitRollback) {
		return ob.ResultDependencyError, ob.ReasonDependencyUnavailable
	}
	return ob.ResultInternalError, ob.ReasonInternal
}

func (s *Service) observeAuth(ctx context.Context, start time.Time, operation ob.AuthOperation, method ob.Method, scope string, pending bool, err error, facts *operationFacts) {
	duration := time.Since(start)
	result, reason := classifyOperation(err)
	grant, mode := ob.GrantNone, ob.RefreshNone
	if err == nil {
		if pending {
			result = ob.ResultPending
		} else {
			grant = observationGrant(scope)
			mode = facts.mode
			if mode == "" {
				mode = ob.RefreshNone
			}
		}
	} else if facts.reason != "" && result != ob.ResultCanceled && result != ob.ResultDeadlineExceeded {
		reason = facts.reason
		if reason == ob.ReasonGraceReadError {
			result = ob.ResultDependencyError
		}
	}
	s.observer.Observe(ctx, ob.AuthObservation{Operation: operation, Method: method, Result: result, Grant: grant, Mode: mode, Reason: reason, Duration: duration})
}

// Public wrappers record only after a normal return, after all implementation
// defers have normalized the final error. Business panics therefore propagate
// unchanged and produce no fabricated terminal observation.

// SignInWithCode 验证码登录；未知身份按策略直接注册或返回待补全结果。
func (s *Service) SignInWithCode(ctx context.Context, cred CodeCredential, dev Device, meta Meta) (SignInResult, error) {
	if !s.observer.Enabled() {
		return s.signInWithCode(ctx, cred, dev, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.signInWithCode(ctx, cred, dev, meta)
	scope := ""
	if result.Tokens != nil {
		scope = result.Tokens.Scope
	}
	s.observeAuth(ctx, start, ob.AuthSignIn, observationMethod(cred.Channel), scope, result.Pending != nil, err, facts)
	return result, err
}

// SignInWithIdp 验证第三方身份，按完整性策略选择登录、直接注册或待补全。
func (s *Service) SignInWithIdp(ctx context.Context, cred IdpCredential, dev Device, meta Meta) (SignInResult, error) {
	if !s.observer.Enabled() {
		return s.signInWithIdp(ctx, cred, dev, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.signInWithIdp(ctx, cred, dev, meta)
	scope := ""
	if result.Tokens != nil {
		scope = result.Tokens.Scope
	}
	s.observeAuth(ctx, start, ob.AuthSignIn, s.observationMethodFor(cred.Kind), scope, result.Pending != nil, err, facts)
	return result, err
}

// CompleteSignIn 先持久化成功证明，再由独立事务完成账号、身份与会话。
func (s *Service) CompleteSignIn(ctx context.Context, token string, cred CodeCredential, dev Device, meta Meta) (SignInResult, error) {
	if !s.observer.Enabled() {
		return s.completeSignIn(ctx, token, cred, dev, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.completeSignIn(ctx, token, cred, dev, meta)
	scope := ""
	if result.Tokens != nil {
		scope = result.Tokens.Scope
	}
	s.observeAuth(ctx, start, ob.AuthCompleteSignIn, observationMethod(cred.Channel), scope, result.Pending != nil, err, facts)
	return result, err
}

// Refresh 实现 refresh_token grant。
func (s *Service) Refresh(ctx context.Context, token string, meta Meta) (TokenResult, error) {
	if !s.observer.Enabled() {
		return s.refresh(ctx, token, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.refresh(ctx, token, meta)
	s.observeAuth(ctx, start, ob.AuthRefresh, ob.MethodNone, result.Scope, false, err, facts)
	return result, err
}

// Reauthenticate 校验 REAUTH 码，刷新会话的 auth_time 并重签 access。
func (s *Service) Reauthenticate(ctx context.Context, p Principal, cred CodeCredential, meta Meta) (TokenResult, error) {
	if !s.observer.Enabled() {
		return s.reauthenticate(ctx, p, cred, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.reauthenticate(ctx, p, cred, meta)
	s.observeAuth(ctx, start, ob.AuthReauthenticate, observationMethod(cred.Channel), result.Scope, false, err, facts)
	return result, err
}

// Authenticate 校验 access token 并返回 Principal。吊销集不可用时 fail-open。
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if !s.observer.Enabled() {
		return s.authenticate(ctx, token)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.authenticate(ctx, token)
	s.observeAuth(ctx, start, ob.AuthAuthenticate, ob.MethodNone, result.Scope, false, err, facts)
	return result, err
}

// AuthenticateStrict 校验 access token，并以已提交的数据库会话状态判定吊销（不读 Redis 吊销集）。
// 会话不存在、不属于 sub 或已吊销返回 ErrInvalidToken；数据库不可用返回 ErrUnavailable，不放行。
// 用于内省及需要撤销后立即失效的关键路由；刷新期限已到但未吊销的会话仍有效。
func (s *Service) AuthenticateStrict(ctx context.Context, token string) (Principal, error) {
	if !s.observer.Enabled() {
		return s.authenticateStrict(ctx, token)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.authenticateStrict(ctx, token)
	s.observeAuth(ctx, start, ob.AuthAuthenticate, ob.MethodNone, result.Scope, false, err, facts)
	return result, err
}

func (s *Service) observeSend(ctx context.Context, start time.Time, channel enum.IdentityKind, purpose enum.CodePurpose, err error, facts *operationFacts) {
	duration := time.Since(start)
	result, reason := classifyOperation(err)
	if err != nil && facts.reason != "" && result != ob.ResultCanceled && result != ob.ResultDeadlineExceeded {
		reason = facts.reason
	}
	s.observer.Observe(ctx, ob.CodeObservation{Operation: ob.CodeSend, Channel: observationChannel(channel), Purpose: observationPurpose(purpose), Result: result, Reason: reason, Duration: duration})
}

// SendSignInCode 发送登录验证码（purpose SIGN_IN）。额度先占后发，发送失败不退还。
func (s *Service) SendSignInCode(ctx context.Context, channel enum.IdentityKind, target string, meta Meta) (CodeChallenge, error) {
	if !s.observer.Enabled() {
		return s.sendSignInCode(ctx, channel, target, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.sendSignInCode(ctx, channel, target, meta)
	s.observeSend(ctx, start, channel, enum.PurposeSignIn, err, facts)
	return result, err
}

// SendBindCode 向即将绑定的新锚点发送 BIND 码。不预检占用（避免枚举），冲突在绑定时报告。
func (s *Service) SendBindCode(ctx context.Context, p Principal, channel enum.IdentityKind, target string, meta Meta) (CodeChallenge, error) {
	if !s.observer.Enabled() {
		return s.sendBindCode(ctx, p, channel, target, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.sendBindCode(ctx, p, channel, target, meta)
	s.observeSend(ctx, start, channel, enum.PurposeBind, err, facts)
	return result, err
}

// SendReauthenticationCode 向用户自己的锚点身份发送 REAUTH 码。
func (s *Service) SendReauthenticationCode(ctx context.Context, p Principal, channel enum.IdentityKind, target string, meta Meta) (CodeChallenge, error) {
	if !s.observer.Enabled() {
		return s.sendReauthenticationCode(ctx, p, channel, target, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.sendReauthenticationCode(ctx, p, channel, target, meta)
	s.observeSend(ctx, start, channel, enum.PurposeReauth, err, facts)
	return result, err
}

// SendSignInCompletionCode 仅向本流程发送 PHONE/EMAIL 补全验证码，不预查询目标归属。
func (s *Service) SendSignInCompletionCode(ctx context.Context, token string, channel enum.IdentityKind, target string, dev Device, meta Meta) (CodeChallenge, error) {
	if !s.observer.Enabled() {
		return s.sendSignInCompletionCode(ctx, token, channel, target, dev, meta)
	}
	start := time.Now()
	ctx, facts := s.observationContext(ctx)
	result, err := s.sendSignInCompletionCode(ctx, token, channel, target, dev, meta)
	s.observeSend(ctx, start, channel, enum.PurposeSignInComplete, err, facts)
	return result, err
}

func dependencyResult(err error) ob.Result {
	if err == nil {
		return ob.ResultSuccess
	}
	result, _ := classifyOperation(err)
	switch result {
	case ob.ResultCanceled, ob.ResultDeadlineExceeded, ob.ResultRejected:
		return result
	default:
		return ob.ResultError
	}
}

func observedDependency[T any](s *Service, ctx context.Context, component ob.Component, operation ob.DependencyOperation, fn func() (T, error)) (T, error) {
	if !s.observer.Enabled() {
		return fn()
	}
	start := time.Now()
	value, err := fn()
	result := dependencyResult(err)
	// Only credential/policy refusals of issue/verify are dependency rejections.
	if result == ob.ResultRejected && component != ob.ComponentCodeStore && component != ob.ComponentIDP {
		result = ob.ResultError
	}
	s.observer.Observe(ctx, ob.DependencyObservation{Component: component, Operation: operation, Result: result, Duration: time.Since(start)})
	return value, err
}

func (s *Service) issueChallenge(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, ip string, binding code.Binding, deadline time.Time) (code.Issued, error) {
	return observedDependency(s, ctx, ob.ComponentCodeStore, ob.DependencyIssue, func() (code.Issued, error) {
		return s.d.Codes.IssueChallengeUntil(ctx, channel, purpose, target, ip, binding, deadline)
	})
}

func (s *Service) verifyChallenge(ctx context.Context, cred code.Credential) error {
	_, err := s.verifyChallengeWithExpiry(ctx, cred)
	return err
}

func (s *Service) verifyChallengeWithExpiry(ctx context.Context, cred code.Credential) (time.Time, error) {
	if !s.observer.Enabled() {
		return s.d.Codes.VerifyChallengeWithExpiry(ctx, cred)
	}
	start := time.Now()
	expiry, err := s.d.Codes.VerifyChallengeWithExpiry(ctx, cred)
	duration := time.Since(start)
	result, reason := classifyOperation(err)
	s.observer.Observe(ctx, ob.DependencyObservation{Component: ob.ComponentCodeStore, Operation: ob.DependencyVerify, Result: dependencyResult(err), Duration: duration})
	s.observer.Observe(ctx, ob.CodeObservation{Operation: ob.CodeVerify, Channel: observationChannel(cred.Channel), Purpose: observationPurpose(cred.Purpose), Result: result, Reason: reason, Duration: duration})
	return expiry, err
}

func (s *Service) discardChallenge(ctx context.Context, channel enum.IdentityKind, purpose enum.CodePurpose, target, id string) error {
	_, err := observedDependency(s, ctx, ob.ComponentCodeStore, ob.DependencyDiscard, func() (struct{}, error) {
		return struct{}{}, s.d.Codes.DiscardChallenge(ctx, channel, purpose, target, id)
	})
	return err
}

func (s *Service) sendSMS(ctx context.Context, target string, msg sender.CodeMessage) error {
	_, err := observedDependency(s, ctx, ob.ComponentSender, ob.DependencySMS, func() (struct{}, error) { return struct{}{}, s.d.SMS.SendSMS(ctx, target, msg) })
	return err
}

func (s *Service) sendEmail(ctx context.Context, target string, msg sender.CodeMessage) error {
	_, err := observedDependency(s, ctx, ob.ComponentSender, ob.DependencyEmail, func() (struct{}, error) { return struct{}{}, s.d.Email.SendEmail(ctx, target, msg) })
	return err
}

func (s *Service) graceGet(ctx context.Context, hash []byte) (grace.Pair, bool, error) {
	if !s.observer.Enabled() {
		return s.d.Grace.Get(ctx, hash)
	}
	start := time.Now()
	pair, ok, err := s.d.Grace.Get(ctx, hash)
	result := dependencyResult(err)
	if err == nil && !ok {
		result = ob.ResultMiss
	}
	s.observer.Observe(ctx, ob.DependencyObservation{Component: ob.ComponentGraceCache, Operation: ob.DependencyGet, Result: result, Duration: time.Since(start)})
	return pair, ok, err
}

func (s *Service) gracePut(ctx context.Context, hash []byte, pair grace.Pair, ttl time.Duration) error {
	_, err := observedDependency(s, ctx, ob.ComponentGraceCache, ob.DependencyPut, func() (struct{}, error) { return struct{}{}, s.d.Grace.Put(ctx, hash, pair, ttl) })
	return err
}

func (s *Service) revocationCheck(ctx context.Context, sid string) (bool, error) {
	return observedDependency(s, ctx, ob.ComponentRevocation, ob.DependencyCheck, func() (bool, error) { return s.d.Revocation.IsRevoked(ctx, sid) })
}

func (s *Service) revokeObserved(ctx context.Context, sid string, ttl time.Duration) error {
	_, err := observedDependency(s, ctx, ob.ComponentRevocation, ob.DependencyRevoke, func() (struct{}, error) { return struct{}{}, s.d.Revocation.Revoke(ctx, sid, ttl) })
	return err
}

func (s *Service) verifyProvider(ctx context.Context, kind enum.IdentityKind, provider idp.Provider, credential json.RawMessage) (idp.Identity, error) {
	return observedDependency(s, ctx, ob.ComponentIDP, observationDependency(kind, provider), func() (idp.Identity, error) {
		ident, err := provider.Verify(ctx, credential)
		if err != nil {
			return idp.Identity{}, isolateProviderError(kind, err)
		}
		// 宿主提供方可能返回不一致的结果；按凭证无效拒绝，由调用方统一审计。
		if ident.Kind != kind || ident.Subject == "" {
			return idp.Identity{}, fmt.Errorf("%w: provider %s returned a mismatched kind or empty subject", idp.ErrInvalidCredential, kind)
		}
		return ident, nil
	})
}

// observationMethodFor 在内置类型之外区分配置 OIDC 与宿主提供方，均为固定取值。
func (s *Service) observationMethodFor(kind enum.IdentityKind) ob.Method {
	if _, ok := s.providers[kind].(*idp.OIDC); ok {
		return ob.MethodOIDC
	}
	return observationMethod(kind)
}

// observationDependency 只使用固定取值；配置或宿主提供的类型名不进入指标标签。
func observationDependency(kind enum.IdentityKind, provider idp.Provider) ob.DependencyOperation {
	switch kind {
	case enum.IdentityWeChat:
		return ob.DependencyVerifyWechat
	case enum.IdentityApple:
		return ob.DependencyVerifyApple
	case enum.IdentityGoogle:
		return ob.DependencyVerifyGoogle
	case enum.IdentityFacebook:
		return ob.DependencyVerifyFacebook
	case enum.IdentityAlipay:
		return ob.DependencyVerifyAlipay
	case enum.IdentityQQ:
		return ob.DependencyVerifyQQ
	case enum.IdentityWeibo:
		return ob.DependencyVerifyWeibo
	}
	if _, ok := provider.(*idp.OIDC); ok {
		return ob.DependencyVerifyOIDC
	}
	return ob.DependencyVerifyProvider
}

func (s *Service) observeDegradation(ctx context.Context, scenario ob.DegradationScenario) {
	if s.observer.Enabled() {
		s.observer.Observe(ctx, ob.DegradationObservation{Scenario: scenario, Count: 1})
	}
}
