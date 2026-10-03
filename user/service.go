package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/email"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/phone"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"

	"github.com/jackc/pgx/v5"
)

// scope 三档（设计文档 §2.3）。
const (
	ScopeUser     = "user"
	ScopeBind     = "user:bind"
	ScopeUndelete = "user:undelete"
)

const maxDeviceField = 64

// Deps 是 Service 的依赖。
type Deps struct {
	Repo       *Repo
	Codes      *code.Store
	Revocation *revocation.Set
	Grace      *grace.Cache
	Signer     *tokens.Signer
	Cipher     *pii.Cipher
	Digester   *pii.Digester
	SMS        sender.SMSSender
	Email      sender.EmailSender
	Audit      audit.Recorder
	Logger     *slog.Logger

	// WeChat / Apple 为 nil 表示未启用对应 IdP。
	WeChat idp.WeChatVerifier
	Apple  idp.AppleVerifier

	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	RefreshGrace  time.Duration
	CodeTTL       time.Duration
	DefaultRegion string
	Now           func() time.Time
	// ReauthMaxAge 为敏感操作的近期认证窗口；零值默认五分钟，负值无效。
	ReauthMaxAge time.Duration
	// SensitiveOpVerification 为 nil 时默认启用；false 只跳过近期认证检查。
	SensitiveOpVerification *bool

	// MaxIdentitiesPerKind 是每个用户、每种身份 kind 的活动身份数上限，必须 ≥ 1。
	MaxIdentitiesPerKind int
	// DeletionCoolingPeriod 是软删除到 purge 的冷静期（purge_time = delete_time + 该值），必须 > 0。
	DeletionCoolingPeriod time.Duration
	// Anonymizers 是宿主业务域的匿名化器，purge 时在库表匿名化之后按顺序、同一事务内执行；可空。
	Anonymizers []anonymize.Anonymizer
}

// Service 持有用户域的全部业务规则。
type Service struct{ d Deps }

// NewService 校验依赖并构造。
func NewService(d Deps) (*Service, error) {
	switch {
	case d.Repo == nil, d.Codes == nil, d.Revocation == nil, d.Grace == nil, d.Signer == nil, d.Cipher == nil, d.Digester == nil, d.SMS == nil, d.Email == nil:
		return nil, errors.New("user: Deps.Repo/Codes/Revocation/Grace/Signer/Cipher/Digester/SMS/Email are required")
	case d.AccessTTL <= 0, d.RefreshTTL <= 0, d.RefreshGrace <= 0, d.CodeTTL <= 0:
		return nil, errors.New("user: AccessTTL/RefreshTTL/RefreshGrace/CodeTTL must be positive")
	case d.DefaultRegion == "":
		return nil, errors.New("user: DefaultRegion is required")
	}
	if d.MaxIdentitiesPerKind < 1 {
		return nil, errors.New("user: Deps.MaxIdentitiesPerKind must be >= 1")
	}
	if d.ReauthMaxAge < 0 {
		return nil, errors.New("user: Deps.ReauthMaxAge must not be negative")
	}
	if d.ReauthMaxAge == 0 {
		d.ReauthMaxAge = 5 * time.Minute
	}
	if d.SensitiveOpVerification == nil {
		enabled := true
		d.SensitiveOpVerification = &enabled
	} else {
		// 保存值快照，避免调用方后续修改指针造成安全策略变化或数据竞争。
		enabled := *d.SensitiveOpVerification
		d.SensitiveOpVerification = &enabled
	}
	if d.DeletionCoolingPeriod <= 0 {
		return nil, errors.New("user: Deps.DeletionCoolingPeriod must be positive")
	}
	names := map[string]struct{}{}
	for i, a := range d.Anonymizers {
		if a == nil {
			return nil, fmt.Errorf("user: Deps.Anonymizers[%d] is nil", i)
		}
		if a.Name() == "" {
			return nil, fmt.Errorf("user: Deps.Anonymizers[%d] has an empty Name", i)
		}
		if _, dup := names[a.Name()]; dup {
			return nil, fmt.Errorf("user: Deps.Anonymizers: duplicate name %q", a.Name())
		}
		names[a.Name()] = struct{}{}
	}
	if d.Audit == nil {
		d.Audit = audit.Noop{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}, nil
}

// Device 是客户端设备身份（X-Device-Id / X-Device-Name）。
type Device struct {
	ID   string
	Name string
}

// Meta 是请求元数据，用于审计与限流。
type Meta struct {
	IP        string
	RequestID string
}

// Principal 是已通过校验的 access token 主体。
type Principal struct {
	UserID    string
	SessionID string
	Scope     string
	AuthTime  time.Time
}

// TokenResult 是签发结果（RFC 6749 字段语义）。
type TokenResult struct {
	AccessToken      string
	RefreshToken     string
	ExpiresIn        int
	RefreshExpiresIn int
	Scope            string
	UserID           string
	IsNewUser        bool
	// HintEmail 是 Apple 已验证的邮箱（含 Apple 私密转发地址），仅供客户端预填绑定页，不代表已绑定。
	HintEmail string
}

func (s *Service) now() time.Time { return s.d.Now() }

// deriveScope 由账号状态与是否有锚点实时推导 scope（不落库）。
func deriveScope(state enum.UserState, hasAnchor bool) string {
	switch {
	case state == enum.UserPendingDeletion:
		return ScopeUndelete
	case !hasAnchor:
		return ScopeBind
	default:
		return ScopeUser
	}
}

// normalizeTarget 按渠道归一化并计算检索提示。
func (s *Service) normalizeTarget(channel enum.IdentityKind, target string) (normalized, hintPrefix, hintSuffix string, err error) {
	switch channel {
	case enum.IdentityPhone:
		e164, err := phone.Normalize(target, s.d.DefaultRegion)
		if err != nil {
			return "", "", "", fmt.Errorf("%w: %v", ErrInvalidTarget, err)
		}
		p, sfx, err := phone.Hints(e164)
		if err != nil {
			return "", "", "", fmt.Errorf("%w: %v", ErrInvalidTarget, err)
		}
		return e164, p, sfx, nil
	case enum.IdentityEmail:
		addr, err := email.Normalize(target)
		if err != nil {
			return "", "", "", fmt.Errorf("%w: %v", ErrInvalidTarget, err)
		}
		p, sfx, err := email.Hints(addr)
		if err != nil {
			return "", "", "", fmt.Errorf("%w: %v", ErrInvalidTarget, err)
		}
		return addr, p, sfx, nil
	default:
		return "", "", "", fmt.Errorf("%w: kind %s is not a code channel", ErrInvalidArgument, channel)
	}
}

func validDevice(dev Device) error {
	if dev.ID == "" || len(dev.ID) > maxDeviceField || len(dev.Name) > maxDeviceField {
		return fmt.Errorf("%w: device id required (<= %d chars), device name <= %d chars", ErrInvalidArgument, maxDeviceField, maxDeviceField)
	}
	return nil
}

// record 填充审计事件的时间与默认 actor 后交给 Recorder。
func (s *Service) record(ctx context.Context, e audit.Event) {
	if e.Actor == enum.ActorKindUnspecified {
		e.Actor = enum.ActorUser
	}
	if e.OccurTime.IsZero() {
		e.OccurTime = s.now()
	}
	s.d.Audit.Record(ctx, e)
}

// revokeInSet 把 sid 写入 Redis 吊销集；失败只记日志（fail-open：吊销退化为最多一个 access TTL）。
// TTL 必须覆盖 access TTL 之后 Signer.Parse 仍会接受的验签 leeway 窗口，否则吊销集条目会先于
// 一个刚好落在 leeway 内的旧 token 过期，让该 token 在窗口内重新被判定为有效。
func (s *Service) revokeInSet(ctx context.Context, sid string) {
	ttl := s.d.AccessTTL + s.d.Signer.Leeway()
	if err := s.d.Revocation.Revoke(ctx, sid, ttl); err != nil {
		s.d.Logger.Warn("user: revocation set write failed (fail-open)", "sid", sid, "err", err)
	}
}

// newRefreshToken 生成 32 字节随机 refresh token（base64url）及其 SHA-256。
func newRefreshToken() (string, []byte, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, fmt.Errorf("user: refresh token: %w", err)
	}
	plain := base64.RawURLEncoding.EncodeToString(b[:])
	return plain, hashRefresh(plain), nil
}

func hashRefresh(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// createSession 新建会话行；返回会话与 refresh 明文（明文只在此刻存在，库中只有哈希）。
func (s *Service) createSession(ctx context.Context, q *db.Queries, userID string, dev Device, now time.Time) (db.Session, string, error) {
	sid, err := ids.New(ids.Session)
	if err != nil {
		return db.Session{}, "", err
	}
	plain, hash, err := newRefreshToken()
	if err != nil {
		return db.Session{}, "", err
	}
	var name *string
	if dev.Name != "" {
		name = &dev.Name
	}
	sess, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID: sid, UserID: userID, DeviceID: dev.ID, DeviceName: name,
		AuthTime: now, RefreshTokenHash: hash, RefreshExpireTime: now.Add(s.d.RefreshTTL),
	})
	if err != nil {
		return db.Session{}, "", fmt.Errorf("user: create session: %w", err)
	}
	return sess, plain, nil
}

// tokensFor 为会话签发 access（auth_time 取自会话）并组装结果。
func (s *Service) tokensFor(sess db.Session, refreshPlain, scope string, now time.Time) (TokenResult, error) {
	access, _, err := s.d.Signer.Sign(tokens.Claims{UserID: sess.UserID, SessionID: sess.ID, Scope: scope, AuthTime: sess.AuthTime}, now, s.d.AccessTTL)
	if err != nil {
		return TokenResult{}, fmt.Errorf("user: sign access: %w", err)
	}
	res := TokenResult{AccessToken: access, ExpiresIn: int(s.d.AccessTTL.Seconds()), Scope: scope, UserID: sess.UserID}
	if refreshPlain != "" {
		res.RefreshToken = refreshPlain
		// 数据库时间戳按微秒舍入，Sub(...).Seconds() 直接截断会把 720h 报成 2591999 而非
		// 2592000；四舍五入到最近的整秒，与 ExpiresIn（本就是整数 TTL）保持一致的语义。
		res.RefreshExpiresIn = int(math.Round(sess.RefreshExpireTime.Sub(now).Seconds()))
	}
	return res, nil
}

// withRetryOnUnique 在唯一冲突（TSID 碰撞、并发首次登录同一 subject）时整体重试一次。
func (s *Service) withRetryOnUnique(ctx context.Context, fn func(q *db.Queries) error) error {
	err := s.d.Repo.WithTx(ctx, fn)
	if IsUniqueViolation(err) {
		s.d.Logger.Info("user: unique violation, retrying transaction once")
		err = s.d.Repo.WithTx(ctx, fn)
	}
	return err
}

// establishSession 是所有登录路径的公共尾部（须在 withRetryOnUnique 的事务内调用）：
// 账号状态检查 → 吊销同设备旧会话 → 建会话 → 签发。replaced 为被替换的会话 id（事务提交后由调用方写吊销集与审计）。
func (s *Service) establishSession(ctx context.Context, q *db.Queries, u db.UserAccount, hasAnchor bool, dev Device, now time.Time) (db.Session, TokenResult, []string, error) {
	switch u.State {
	case enum.UserFrozen:
		return db.Session{}, TokenResult{}, nil, ErrUserFrozen
	case enum.UserDeleted:
		// 不可达路径：阶段 5 的 purge 必须在同一事务内把 state 置为 DELETED 并匿名化该用户所有身份；
		// 一个仍可命中的活跃身份挂在 DELETED 用户下说明 purge 破坏了不变式。
		return db.Session{}, TokenResult{}, nil, fmt.Errorf("user: invariant violated: deleted user %s still has an active identity", u.ID)
	}
	var replaced []string
	if old, err := q.GetActiveSessionByUserDevice(ctx, db.GetActiveSessionByUserDeviceParams{UserID: u.ID, DeviceID: dev.ID}); err == nil {
		reason := enum.RevokeReplacedByRelogin
		if _, err := q.RevokeSession(ctx, db.RevokeSessionParams{ID: old.ID, Reason: &reason, Now: now}); err != nil {
			return db.Session{}, TokenResult{}, nil, fmt.Errorf("user: revoke replaced session: %w", err)
		}
		replaced = append(replaced, old.ID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return db.Session{}, TokenResult{}, nil, fmt.Errorf("user: lookup device session: %w", err)
	}
	sess, refreshPlain, err := s.createSession(ctx, q, u.ID, dev, now)
	if err != nil {
		return db.Session{}, TokenResult{}, nil, err
	}
	res, err := s.tokensFor(sess, refreshPlain, deriveScope(u.State, hasAnchor), now)
	if err != nil {
		return db.Session{}, TokenResult{}, nil, err
	}
	return sess, res, replaced, nil
}

// finishSignIn 在事务提交后处理被替换会话的吊销集与审计，并记录 SIGN_IN。
func (s *Service) finishSignIn(ctx context.Context, ev audit.Event, res TokenResult, replaced []string, dev Device, meta Meta) {
	for _, sid := range replaced {
		s.revokeInSet(ctx, sid)
		s.record(ctx, audit.Event{Type: enum.EventSessionRevoked, Result: enum.ResultSuccess, Reason: enum.RevokeReplacedByRelogin.String(), UserID: ev.UserID, SessionID: sid, IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID})
	}
	ev.Type, ev.Result = enum.EventSignIn, enum.ResultSuccess
	if res.IsNewUser {
		ev.Reason = "NEW_USER"
	}
	s.record(ctx, ev)
}
