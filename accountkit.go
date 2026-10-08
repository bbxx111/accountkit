package accountkit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/admin"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/maintenance"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/tokens"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
	"github.com/bbxx111/accountkit/user/sender"
)

// ErrSearchPath 表示 Deps.Pool 的连接 search_path 首位不是 Config.Schema。
// 迁移 SQL 与查询都用非限定表名，search_path 不对会把对象建到别的 schema 里。
var ErrSearchPath = errors.New("accountkit: pool search_path does not include the configured schema")

// AdminVerifier 是宿主管理端 OIDC verifier 的最小契约（路由级角色中间件）；本模块不 import oidc-verifier，
// *oidcverifier.Verifier 直接满足它。
type AdminVerifier = admin.Verifier

// AdminPrincipal 是管理端主体；宿主从 oidcverifier.PrincipalFrom 映射后经 Deps.AdminPrincipal 提供。
type AdminPrincipal = admin.Principal

// Deps 是宿主注入的依赖。
type Deps struct {
	// Pool 是 PostgreSQL 连接池。连接的 search_path 首位须为 Config.Schema（用 PoolConfig 构造即可）。
	Pool *pgxpool.Pool
	// Redis 用于验证码、额度、宽限缓存、吊销集等短生命周期数据。
	Redis redis.UniversalClient
	// Logger 默认 slog.Default()。
	Logger *slog.Logger
	// ClientIP 从请求解析来源 IP（限流用）。默认取 RemoteAddr 的 host；
	// 部署在代理后时宿主应注入按可信跳数解析 X-Forwarded-For 的实现。
	ClientIP func(*http.Request) string
	// RequestID 从请求取关联 id。默认读 X-Request-Id（须匹配 ^[A-Za-z0-9._-]{1,64}$），缺失或非法则生成。
	RequestID func(*http.Request) string
	// SMSSender / EmailSender 投递验证码。必填；开发环境可用 sender.NewLog(logger)，生产必须换成服务商实现。
	SMSSender   sender.SMSSender
	EmailSender sender.EmailSender
	// Audit 接收审计事件。为空时默认使用 audit.Async（有界队列 + 批量写入 audit_event 表；Start 启动、Close 刷出）；
	// 宿主可注入自己的 Recorder（例如转发到日志管道）。任何实现都不得让记录失败影响认证动作的结果。
	Audit audit.Recorder
	// HTTPClient 用于调用微信 API 与拉取 Apple JWKS。默认 10 秒超时；宿主可注入带代理/追踪的客户端。
	HTTPClient *http.Client
	// Anonymizers 是宿主业务域的匿名化器：purge 到期账号时，在库表匿名化之后按此顺序、同一事务内调用。
	// 可空。业务表须与库表同库；Name 须唯一。
	Anonymizers []anonymize.Anonymizer
	// BeforeDelete 在注销事务中、账号锁和 ACTIVE 校验之后执行宿主检查；可空。
	BeforeDelete user.BeforeDelete
	// AdminVerifier / AdminPrincipal 启用管理面（§4.2/§6.3）：宿主在 /admin/v1 先挂 verifier 的 Middleware，
	// 再 Mount AdminHandler()；库内每条路由用 AdminVerifier.RequireRole 放行，并用 AdminPrincipal 取当前管理员。
	// 二者须同时提供；都为空时 AdminHandler() 的所有路由返回 503 ADMIN_NOT_CONFIGURED。
	AdminVerifier  AdminVerifier
	AdminPrincipal func(ctx context.Context) (AdminPrincipal, bool)
}

// Auth 是 accountkit 的门面。用 New 构造。
type Auth struct {
	cfg      Config
	deps     Deps
	cipher   *pii.Cipher
	digester *pii.Digester
	signer   *tokens.Signer
	runner   *maintenance.Runner

	auditStore *audit.Store
	auditAsync *audit.Async

	codes        *code.Store
	revocation   *revocation.Set
	grace        *grace.Cache
	users        *user.Service
	endUser      *enduser.Handler
	adminHandler http.Handler

	startOnce sync.Once
	closeOnce sync.Once
}

// New 应用默认、校验配置与依赖，构造密码学原语与维护运行器。不做任何 I/O。
func New(cfg Config, deps Deps) (*Auth, error) {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Pool == nil {
		return nil, errors.New("accountkit: Deps.Pool is required")
	}
	if deps.Redis == nil {
		return nil, errors.New("accountkit: Deps.Redis is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.ClientIP == nil {
		deps.ClientIP = defaultClientIP
	}
	if deps.RequestID == nil {
		deps.RequestID = defaultRequestID
	}
	if deps.SMSSender == nil || deps.EmailSender == nil {
		return nil, errors.New("accountkit: Deps.SMSSender and Deps.EmailSender are required (use sender.NewLog for development)")
	}
	if deps.HTTPClient == nil {
		deps.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if (deps.AdminVerifier == nil) != (deps.AdminPrincipal == nil) {
		return nil, errors.New("accountkit: Deps.AdminVerifier and Deps.AdminPrincipal must be provided together")
	}
	cipher, err := pii.NewCipher(cfg.SubjectCipherKeys, cfg.SubjectCipherActiveKey)
	if err != nil {
		return nil, err
	}
	digester, err := pii.NewDigester(cfg.SubjectHMACKeys, cfg.SubjectHMACActiveKey)
	if err != nil {
		return nil, err
	}
	signer, err := tokens.NewSigner(tokens.Options{
		Keys: cfg.JWTKeys, Active: cfg.JWTActiveKey, Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience,
	})
	if err != nil {
		return nil, err
	}
	auditStore := audit.NewStore(deps.Pool)
	var auditAsync *audit.Async
	if deps.Audit == nil {
		auditAsync = audit.NewAsync(auditStore, audit.AsyncOptions{Logger: deps.Logger})
		deps.Audit = auditAsync
	}
	a := &Auth{cfg: cfg, deps: deps, cipher: cipher, digester: digester, signer: signer, auditStore: auditStore, auditAsync: auditAsync}
	a.codes = code.NewStore(deps.Redis, cfg.KeyPrefix, digester, code.Options{
		TTL: cfg.CodeTTL, Cooldown: cfg.CodeCooldown, MaxAttempts: cfg.CodeMaxAttempts,
		DailyLimitPerTarget: cfg.CodeDailyLimitPerTarget, DailyLimitPerIP: cfg.CodeDailyLimitPerIP,
		FailureLimitPerTarget: cfg.CodeFailureLimitPerTarget, FailureWindow: cfg.CodeFailureWindow,
	})
	a.revocation = revocation.NewSet(deps.Redis, cfg.KeyPrefix)
	a.grace = grace.NewCache(deps.Redis, cfg.KeyPrefix, cipher)
	var wechat idp.WeChatVerifier
	if len(cfg.WeChatApps) > 0 {
		apps := make([]idp.App, len(cfg.WeChatApps))
		for i, a := range cfg.WeChatApps {
			apps[i] = idp.App{AppID: a.AppID, Secret: a.Secret}
		}
		wechat = idp.NewWeChat(apps, cfg.WeChatAPIBaseURL, deps.HTTPClient, deps.Logger)
	}
	var apple idp.AppleVerifier
	if len(cfg.AppleBundleIDs) > 0 {
		apple = idp.NewApple(idp.AppleOptions{
			BundleIDs: cfg.AppleBundleIDs, JWKSURL: cfg.AppleJWKSURL, HTTP: deps.HTTPClient,
			Nonces: idp.NewNonceRegistry(deps.Redis, cfg.KeyPrefix), Logger: deps.Logger, NonceTTL: cfg.AppleNonceTTL,
		})
	}
	users, err := user.NewService(user.Deps{
		Repo: user.NewRepo(deps.Pool), Codes: a.codes, Revocation: a.revocation, Grace: a.grace,
		Signer: signer, Cipher: cipher, Digester: digester,
		SMS: deps.SMSSender, Email: deps.EmailSender, Audit: deps.Audit, Logger: deps.Logger,
		WeChat: wechat, Apple: apple,
		AccessTTL: cfg.AccessTokenTTL, RefreshTTL: cfg.RefreshTokenTTL, RefreshGrace: cfg.RefreshGrace, CodeTTL: cfg.CodeTTL,
		DefaultRegion:           cfg.DefaultRegion,
		ReauthMaxAge:            cfg.ReauthMaxAge,
		SensitiveOpVerification: cfg.SensitiveOpVerification,
		MaxIdentitiesPerKind:    cfg.MaxIdentitiesPerKind,
		DeletionCoolingPeriod:   cfg.DeletionCoolingPeriod,
		Anonymizers:             deps.Anonymizers,
		BeforeDelete:            deps.BeforeDelete,
	})
	if err != nil {
		return nil, err
	}
	a.users = users
	h, err := enduser.New(enduser.Deps{
		Users: users, Logger: deps.Logger, ClientIP: deps.ClientIP, RequestID: deps.RequestID,
		ReauthMaxAge: cfg.ReauthMaxAge, SensitiveOpVerification: *cfg.SensitiveOpVerification,
	})
	if err != nil {
		return nil, err
	}
	a.endUser = h
	if deps.AdminVerifier != nil {
		ah, err := admin.New(admin.Deps{
			Users: users, Audit: auditStore, Verifier: deps.AdminVerifier, Principal: deps.AdminPrincipal,
			Logger: deps.Logger, ClientIP: deps.ClientIP, RequestID: deps.RequestID,
		})
		if err != nil {
			return nil, err
		}
		a.adminHandler = ah.Router()
	} else {
		a.adminHandler = admin.Unconfigured(deps.RequestID)
	}
	// 维护任务（§5.6 #1–#5）：顺序执行、失败互不影响。
	a.runner = maintenance.NewRunner(cfg.MaintenanceInterval, maintenance.NewPGLocker(deps.Pool, cfg.Schema), deps.Logger,
		maintenance.Task{Name: "purge_users", Run: func(ctx context.Context) error {
			n, err := users.PurgeDueUsers(ctx)
			if n > 0 {
				deps.Logger.Info("maintenance: purged users", "count", n)
			}
			return err
		}},
		maintenance.Task{Name: "cleanup_sessions", Run: func(ctx context.Context) error {
			n, err := users.CleanupSessions(ctx)
			if n > 0 {
				deps.Logger.Info("maintenance: deleted stale sessions", "count", n)
			}
			return err
		}},
		maintenance.Task{Name: "audit_retention", Run: func(ctx context.Context) error {
			before := time.Now().Add(-time.Duration(cfg.AuditRetentionDays) * 24 * time.Hour)
			n, err := auditStore.DeleteOlderThan(ctx, before)
			if n > 0 {
				deps.Logger.Info("maintenance: deleted expired audit events", "count", n)
			}
			return err
		}},
		maintenance.Task{Name: "rekey_digests", Run: func(ctx context.Context) error {
			n, err := users.RekeyDigests(ctx)
			if n > 0 {
				deps.Logger.Info("maintenance: rekeyed identity digests", "count", n)
			}
			return err
		}},
		maintenance.Task{Name: "reencrypt_subjects", Run: func(ctx context.Context) error {
			n, err := users.ReencryptSubjects(ctx)
			if n > 0 {
				deps.Logger.Info("maintenance: re-encrypted identity subjects", "count", n)
			}
			return err
		}},
	)
	return a, nil
}

// Users 返回用户域服务，供 HTTP 层与宿主使用。
func (a *Auth) Users() *user.Service { return a.users }

// EndUserHandler 返回 C 端相对路由（宿主 Mount 到 /v1）。
func (a *Auth) EndUserHandler() http.Handler { return a.endUser.Router() }

// AdminHandler 返回管理面相对路由（宿主在 verifier Middleware 之后 Mount 到 /admin/v1）。
// 未配置 Deps.AdminVerifier / AdminPrincipal 时所有路由 503 ADMIN_NOT_CONFIGURED。
func (a *Auth) AdminHandler() http.Handler { return a.adminHandler }

// RecordAdminForbidden 记录 ADMIN_FORBIDDEN（有效 token 但角色不足，§5.1）。供宿主接到 verifier 的 OnForbidden 钩子：
// 拦截本身由 verifier 完成，这里只写审计（actor ADMIN，reason ROLE_REQUIRED，ip 与 request_id 取自请求）。
func (a *Auth) RecordAdminForbidden(r *http.Request, p AdminPrincipal) {
	a.deps.Audit.Record(r.Context(), audit.Event{
		Type: enum.EventAdminForbidden, Actor: enum.ActorAdmin, Result: enum.ResultFailure, Reason: "ROLE_REQUIRED",
		AdminIssuer: p.Issuer, AdminSubject: p.Subject, AdminUsername: p.Username,
		IP: a.deps.ClientIP(r), RequestID: a.deps.RequestID(r), OccurTime: time.Now(),
	})
}

// RequireScope 返回“Bearer 认证 + scope 检查”中间件，供宿主业务路由使用。
func (a *Auth) RequireScope(allowed ...string) func(http.Handler) http.Handler {
	return authn.RequireScope(a.endUser.AuthnOptions(), allowed...)
}

// RequireRecentAuth 返回近期认证中间件（Config.ReauthMaxAge / SensitiveOpVerification）。
func (a *Auth) RequireRecentAuth() func(http.Handler) http.Handler {
	return a.endUser.RequireRecentAuth()
}

// PrincipalFrom 取出请求上下文中的 Principal。
func PrincipalFrom(ctx context.Context) (user.Principal, bool) { return authn.PrincipalFrom(ctx) }

// Config 返回应用默认值后的配置副本。
func (a *Auth) Config() Config { return a.cfg }

// Migrate 校验 search_path 与 Redis 连通性，创建 schema 并应用迁移，然后校验 identity 中使用的密钥版本
// 都在配置内（在回填完成前移除旧密钥 → 启动失败，而不是第一次登录时失败）。
func (a *Auth) Migrate(ctx context.Context) error {
	if err := a.checkSearchPath(ctx); err != nil {
		return err
	}
	if err := a.deps.Redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("accountkit: redis ping: %w", err)
	}
	if err := migrations.Up(ctx, a.deps.Pool.Config().ConnConfig, a.cfg.Schema); err != nil {
		return err
	}
	if err := a.users.CheckKeyVersions(ctx); err != nil {
		return fmt.Errorf("accountkit: %w", err)
	}
	return nil
}

// checkSearchPath 要求连接 search_path 的第一项是配置的 schema。
func (a *Auth) checkSearchPath(ctx context.Context) error {
	var sp string
	if err := a.deps.Pool.QueryRow(ctx, `SHOW search_path`).Scan(&sp); err != nil {
		return fmt.Errorf("accountkit: show search_path: %w", err)
	}
	first := strings.TrimSpace(strings.Split(sp, ",")[0])
	first = strings.Trim(first, `"`)
	if first != a.cfg.Schema {
		return fmt.Errorf("%w: search_path is %q, want it to start with %q (use accountkit.PoolConfig)", ErrSearchPath, sp, a.cfg.Schema)
	}
	return nil
}

// Start 启动审计 worker 与维护任务 ticker；重复调用只启动一次。
func (a *Auth) Start(ctx context.Context) {
	a.startOnce.Do(func() {
		if a.auditAsync != nil {
			a.auditAsync.Start(ctx)
		}
		a.runner.Start(ctx)
	})
}

// Close 先停维护任务再关审计（让最后一轮维护产生的事件也能刷出）；幂等。未 Start 过也安全。
func (a *Auth) Close() {
	a.closeOnce.Do(func() {
		a.runner.Close()
		if a.auditAsync != nil {
			a.auditAsync.Close()
		}
	})
}

// RunMaintenanceOnce 立即执行一轮维护任务（抢 advisory lock → purge_users → cleanup_sessions → audit_retention →
// rekey_digests → reencrypt_subjects），返回是否抢到锁。供宿主的一次性作业与测试使用；Start 的 ticker 每轮做的就是这件事。
// 须在 Close 之前调用：Close 之后维护任务产生的审计事件会被丢弃并计数。
func (a *Auth) RunMaintenanceOnce(ctx context.Context) bool { return a.runner.RunOnce(ctx) }

// PoolConfig 解析 dsn 并把 search_path 设为 "<schema>,public"，供宿主构造 pgxpool。
func PoolConfig(dsn, schema string) (*pgxpool.Config, error) {
	if !migrations.ValidSchema(schema) {
		return nil, fmt.Errorf("accountkit: invalid schema name %q", schema)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("accountkit: parse dsn: %w", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	return cfg, nil
}

func defaultClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func defaultRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); requestIDRe.MatchString(id) {
		return id
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
