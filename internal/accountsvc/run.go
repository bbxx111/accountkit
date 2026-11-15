package accountsvc

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
	"github.com/bbxx111/accountkit/internal/accountsvc/identityresolve"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
	"github.com/bbxx111/accountkit/internal/accountsvc/oidcjwt"
	"github.com/bbxx111/accountkit/internal/accountsvc/purgefeed"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/bbxx111/accountkit/user/sender/smtp"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// 以下工厂抽象运行时拥有的资源及生命周期，不增加宿主公共接口。
type dependencies struct {
	pool         *pgxpool.Pool
	redis        *redis.Client
	databasePing func(context.Context) error
	redisPing    func(context.Context) error
	closeRedis   func() error
	closePool    func()
}
type application struct {
	migrate            func(context.Context) error
	preloadAdminTokens func(context.Context) error
	closeTokens        func()
	// 两类密钥均在监听就绪后后台预加载，失败只记录固定分类日志。
	preloadServiceTokens       func(context.Context) error
	start                      func(context.Context)
	close                      func()
	endUser, admin, introspect http.Handler
	purgeFeed                  http.Handler
	identityResolve            http.Handler
	jwks                       http.Handler
	metrics                    http.Handler
}
type runtimeFactory struct {
	open   func(context.Context, Config, *slog.Logger) (*dependencies, error)
	newApp func(Config, *dependencies, *slog.Logger) (*application, error)
	listen func(context.Context, string) (net.Listener, error)
}

func parseCommand(args []string) (string, error) {
	if len(args) == 0 {
		return "serve", nil
	}
	if len(args) == 1 && (args[0] == "serve" || args[0] == "migrate") {
		return args[0], nil
	}
	return "", errors.New("accountsvc: command must be serve, migrate or email-templates check")
}

// Run 接收主入口的信号 context，执行安全配置校验与完整服务生命周期。
// `email-templates check` 是离线子命令：不读取服务配置，也不创建依赖。
func Run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) > 0 && args[0] == "email-templates" {
		return runEmailTemplatesCommand(args[1:], os.Stdout)
	}
	command, err := parseCommand(args)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(command)
	if err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return runConfig(ctx, cfg, logger, runtimeFactory{open: openDependencies, newApp: newApplication, listen: func(ctx context.Context, addr string) (net.Listener, error) {
		return (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	}})
}

func openDependencies(ctx context.Context, cfg Config, _ *slog.Logger) (*dependencies, error) {
	pc, err := accountkit.PoolConfig(cfg.DatabaseURL, cfg.Library.Schema)
	if err != nil {
		return nil, invalidConfig("ACCOUNTSVC_DATABASE_URL / ACCOUNTSVC_AUTH_SCHEMA")
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, errors.New("accountsvc: create database pool failed")
	}
	d := &dependencies{pool: pool, databasePing: pool.Ping, closePool: pool.Close}
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return d, invalidConfig("ACCOUNTSVC_REDIS_URL")
	}
	opts.ContextTimeoutEnabled = true
	rdb := redis.NewClient(opts)
	d.redis = rdb
	d.redisPing = func(ctx context.Context) error { return rdb.Ping(ctx).Err() }
	d.closeRedis = rdb.Close
	if err = pool.Ping(ctx); err != nil {
		return d, errors.New("accountsvc: database connection failed")
	}
	if err = rdb.Ping(ctx).Err(); err != nil {
		return d, errors.New("accountsvc: Redis connection failed")
	}
	return d, nil
}

// 管理认证在构造时静态装配，远程 discovery 在监听就绪后执行。
func newApplication(cfg Config, d *dependencies, logger *slog.Logger) (*application, error) {
	if cfg.command == "migrate" {
		return newMigrationApplication(cfg, d)
	}
	observer, metrics, err := newMetrics(cfg)
	if err != nil {
		return nil, err
	}
	var email sender.EmailCodeSender
	sms, err := smsSender(cfg, logger)
	if err != nil {
		return nil, err
	}
	if cfg.identityEnabled(enum.IdentityEmail) {
		s, err := smtp.NewWithRenderer(cfg.SMTP, emailRenderer(cfg))
		if err != nil {
			return nil, errors.New("accountsvc: SMTP configuration failed")
		}
		email = s
	}
	// 服务使用默认本地审计读写；Auth 拥有审计 worker，服务拥有 Pool/Redis。
	// 外部审计装配属于嵌入宿主扩展，见 examples/audit，不在此创建远端适配器。
	deps := accountkit.Deps{Pool: d.pool, Redis: d.redis, SMSSender: sms, EmailSender: email, Logger: logger, ClientIP: clientIP, RequestID: requestID, Observer: observer}
	var recordForbidden func(*http.Request, accountkit.AdminPrincipal)
	var adminVerifier *adminauth.Verifier

	if cfg.command == "serve" && cfg.AdminEnabled {
		var err error
		adminVerifier, err = adminauth.New(context.Background(), cfg.Admin, adminauth.Options{Logger: logger, OnForbidden: func(r *http.Request, p accountkit.AdminPrincipal) {
			if recordForbidden != nil {
				recordForbidden(r, p)
			}
		}})
		if err != nil {
			return nil, err
		}
		deps.AdminVerifier = adminVerifier
		deps.AdminPrincipal = adminauth.PrincipalFrom
	}
	auth, err := accountkit.New(cfg.Library, deps)
	if err != nil {
		if adminVerifier != nil {
			adminVerifier.Close()
		}
		return nil, errors.New("accountsvc: accountkit construction failed")
	}
	recordForbidden = auth.RecordAdminForbidden
	var closeServiceTokens func()
	closeTokens := func() {
		if adminVerifier != nil {
			adminVerifier.Close()
		}
		if closeServiceTokens != nil {
			closeServiceTokens()
		}
	}
	closeAuth := func() { closeTokens(); auth.Close() }
	app := &application{migrate: auth.Migrate, start: auth.Start, close: closeAuth, closeTokens: closeTokens, endUser: auth.EndUserHandler(), admin: auth.AdminHandler(), jwks: auth.JWKSHandler(), metrics: metrics}
	if cfg.identityEnabled(enum.IdentityEmail) && emailRenderer(cfg).Multilingual() {
		// 只有多语言清单才需要读取请求语言；发码只经由消费者路由。
		app.endUser = acceptLanguage(app.endUser)
	}
	if cfg.command == "serve" {
		intro, purge, resolve, preload, err := serviceAuthenticators(cfg, logger, nil, &closeServiceTokens)
		if err != nil {
			closeAuth()
			return nil, err
		}
		app.preloadServiceTokens = preload
		namespace := cfg.Library.AuditNamespace
		if namespace == "" {
			namespace = "accountkit/" + cfg.Library.Schema + "/resources"
		}
		app.identityResolve, err = identityresolve.NewWithAuthenticator(resolve, auth.Users().ResolveIdentity, namespace, requestID)
		if err != nil {
			closeAuth()
			return nil, err
		}
		app.introspect, err = introspection.NewWithAuthenticator(intro, auth.Users().AuthenticateStrict)
		if err != nil {
			closeAuth()
			return nil, errors.New("accountsvc: introspection configuration failed")
		}
		app.purgeFeed, err = purgefeed.NewWithAuthenticator(purge, auth.Users().ListPurgedUsers)
		if err != nil {
			closeAuth()
			return nil, errors.New("accountsvc: purged user feed configuration failed")
		}
	}
	if adminVerifier != nil {
		app.admin = adminVerifier.Middleware(auth.AdminHandler())
		app.preloadAdminTokens = adminVerifier.Load
	}
	return app, nil
}

// migrate 只装配存储迁移与密钥核验，不创建认证服务、发送器、IdP 或 HTTP 处理器。
func newMigrationApplication(cfg Config, d *dependencies) (*application, error) {
	if err := cfg.Library.Validate(); err != nil {
		return nil, safeLibraryConfigError(err)
	}
	digester, err := pii.NewDigester(cfg.Library.SubjectHMACKeys, cfg.Library.SubjectHMACActiveKey)
	if err != nil {
		return nil, invalidConfig("ACCOUNTSVC_SUBJECT_HMAC_KEYS / ACCOUNTSVC_SUBJECT_HMAC_ACTIVE_KEY")
	}
	cipher, err := pii.NewCipher(cfg.Library.SubjectCipherKeys, cfg.Library.SubjectCipherActiveKey)
	if err != nil {
		return nil, invalidConfig("ACCOUNTSVC_SUBJECT_CIPHER_KEYS / ACCOUNTSVC_SUBJECT_CIPHER_ACTIVE_KEY")
	}
	return &application{migrate: func(ctx context.Context) error {
		var path string
		if err := d.pool.QueryRow(ctx, "SHOW search_path").Scan(&path); err != nil {
			return errors.New("accountsvc: inspect database search_path failed")
		}
		first := strings.Trim(strings.TrimSpace(strings.Split(path, ",")[0]), `"`)
		if first != cfg.Library.Schema {
			return accountkit.ErrSearchPath
		}
		if err := d.redis.Ping(ctx).Err(); err != nil {
			return errors.New("accountsvc: Redis connection failed")
		}
		if err := migrations.Up(ctx, d.pool.Config().ConnConfig, cfg.Library.Schema); err != nil {
			return err
		}
		return user.CheckKeyVersions(ctx, user.NewRepo(d.pool), digester, cipher, time.Now())
	}}, nil
}

func startupCleanup(cfg Config, d *dependencies, app *application, listeners []net.Listener) error {
	for _, ln := range listeners {
		_ = ln.Close()
	}
	budget := cfg.ShutdownTimeout
	if budget <= 0 {
		budget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	var closeLibrary func()
	if app != nil {
		closeLibrary = app.close
	}
	var closeRedis func() error
	var closePool func()
	if d != nil {
		closeRedis = d.closeRedis
		closePool = d.closePool
	}
	return shutdownRuntime(ctx, 0, nil, &healthState{}, newRequestTracker(), func() {}, closeLibrary, closeRedis, closePool)
}

// serviceTokenRefreshInterval 是服务令牌签发方的刷新冷却：任何一次尝试后窗口内不再拉取。
const serviceTokenRefreshInterval = 10 * time.Second

// serviceTokenRefreshTimeout 小于冷却间隔，使挂起的签发方在窗口内就判定失败，
// 后续 Bearer 请求立即得到 503，而不是每次都等满认证上限。
const serviceTokenRefreshTimeout = 5 * time.Second

// serviceTokenPreloadTimeout 限制后台预加载签发方密钥的等待。
const serviceTokenPreloadTimeout = 10 * time.Second

// serviceAuthenticators 为三个内部服务接口构造调用认证。接口共享一个服务令牌
// verifier（延迟加载）；preload 为 nil 表示未启用 Bearer。client 仅供测试注入。
func serviceAuthenticators(cfg Config, logger *slog.Logger, client *http.Client, closers ...*func()) (intro, purge, resolve *serviceauth.Authenticator, preload func(context.Context) error, err error) {
	var tokens *oidcjwt.Verifier
	registry := cfg.ServiceClients
	if registry == nil {
		registry, _ = serviceauth.NewRegistry(nil)
	}
	bearerEnabled := registry.BearerEnabled()
	if bearerEnabled {
		tokens, err = oidcjwt.New(cfg.Service.Issuer, cfg.Service.Audience, oidcjwt.Options{Profile: cfg.Service.TokenProfile, HTTPClient: client, RefreshInterval: serviceTokenRefreshInterval, RefreshTimeout: serviceTokenRefreshTimeout, Logger: logger, AllowHTTP: cfg.Service.AllowHTTP})
		if err != nil {
			return nil, nil, nil, nil, errors.New("accountsvc: service token configuration failed")
		}
		preload = tokens.Load
		if len(closers) > 0 {
			*closers[0] = tokens.Close
		}
	}
	build := func(id routeID) (*serviceauth.Authenticator, error) {
		route := serviceRoute(id)
		return serviceauth.New(serviceauth.Config{Realm: route.realm, Endpoint: route.endpoint, Logger: logger, Registry: registry, RequiredPermission: route.permission, BearerVerifier: tokens})
	}
	if intro, err = build(routeIntrospect); err != nil {
		return nil, nil, nil, nil, errors.New("accountsvc: introspection configuration failed")
	}
	if purge, err = build(routePurgeFeed); err != nil {
		return nil, nil, nil, nil, errors.New("accountsvc: purged user feed configuration failed")
	}
	if resolve, err = build(routeIdentityResolve); err != nil {
		return nil, nil, nil, nil, err
	}
	return intro, purge, resolve, preload, nil
}

// warnUnconfigured 为未配置、只会返回 503 *_NOT_CONFIGURED 的可选接口各记一条 WARN，
// 使漏配在启动日志中可见，而不是等到调用失败才发现。只写变量名，不写值。
func warnUnconfigured(cfg Config, logger *slog.Logger) {
	if !cfg.ServiceClients.HasPermission("identities.resolve") {
		logger.Warn("accountsvc: identity resolution is not configured; POST /v1/identities:resolve answers 503 IDENTITY_RESOLUTION_NOT_CONFIGURED", "settings", "ACCOUNTSVC_SERVICE_CLIENTS, ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_PERMISSIONS")
	}
	introEnabled := cfg.ServiceClients.HasPermission("tokens.introspect")
	purgeEnabled := cfg.ServiceClients.HasPermission("purged_users.read")
	introSettings := "ACCOUNTSVC_SERVICE_CLIENTS, ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_PERMISSIONS"
	purgeSettings := introSettings
	if !introEnabled {
		logger.Warn("accountsvc: introspection is not configured; POST /v1/introspect answers 503 introspection_not_configured",
			"settings", introSettings)
	}
	if !purgeEnabled {
		logger.Warn("accountsvc: purged user feed is not configured; GET /v1/purgedUsers answers 503 PURGE_FEED_NOT_CONFIGURED",
			"settings", purgeSettings)
	}
	if !cfg.AdminEnabled {
		logger.Warn("accountsvc: administrator API is not configured; /admin/v1 answers 503 ADMIN_NOT_CONFIGURED",
			"settings", "ACCOUNTSVC_ADMIN_ENABLED")
	}
	if cfg.Library.ProfileAttributes == nil {
		logger.Warn("accountsvc: profile attributes are not configured; GET/PATCH /v1/users/me/profile answer 503 PROFILE_ATTRIBUTES_NOT_CONFIGURED",
			"settings", "ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE")
	}
	if cfg.identityEnabled(enum.IdentityEmail) && cfg.EmailTemplates == nil {
		logger.Warn("accountsvc: email templates are not configured; verification emails use the built-in default wording without a brand",
			"settings", "ACCOUNTSVC_SMTP_TEMPLATES_FILE")
	}
}

// warnPlainHTTPIssuers 为 development 模式下使用明文 HTTP 的管理员与服务令牌签发方各记一条 WARN。
// 在任何明文网络访问（管理员 discovery）之前调用，只写变量名，不写值；production 不会到达此处的
// HTTP 分支，因为配置校验已拒绝。
func warnPlainHTTPIssuers(cfg Config, logger *slog.Logger) {
	if cfg.AdminEnabled && plainHTTP(cfg.Admin.Issuer) {
		logger.Warn("accountsvc: administrator issuer uses plain HTTP; allowed only in development mode", "settings", "ACCOUNTSVC_ADMIN_ISSUER")
	}
	if cfg.Service.Enabled() && plainHTTP(cfg.Service.Issuer) {
		logger.Warn("accountsvc: service token issuer uses plain HTTP; allowed only in development mode", "settings", "ACCOUNTSVC_SERVICE_ISSUER")
	}
}

// plainHTTP 按解析后的 scheme 判断，与配置校验一致（url.Parse 会把 HTTP:// 归一为 http）。
func plainHTTP(issuer string) bool {
	u, err := url.Parse(issuer)
	return err == nil && u.Scheme == "http"
}

// preloadServiceTokens 在就绪后于后台尝试加载服务令牌签发方的密钥，返回的函数取消并等待它。
// 签发方故障（含挂起）既不消耗启动预算也不推迟就绪：Bearer 调用在恢复前返回 503，消费者路由与
// Basic 调用照常可用。
func preloadServiceTokens(preload func(context.Context) error, issuer string, logger *slog.Logger) func() {
	return preloadIssuerTokens(preload, issuer, "service token", logger)
}

func preloadIssuerTokens(preload func(context.Context) error, issuer, category string, logger *slog.Logger) (stop func()) {
	if preload == nil {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceTokenPreloadTimeout)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := preload(ctx); err != nil && !errors.Is(err, context.Canceled) {
			if errors.Is(err, oidcjwt.ErrIssuerMismatch) {
				logger.Error("accountsvc: "+category+" issuer discovery does not match configuration", "issuer", issuer)
				return
			}
			logger.Warn("accountsvc: "+category+" issuer unavailable at startup; bearer calls answer 503 until it recovers", "issuer", issuer)
		}
	}()
	return func() { cancel(); <-done }
}

func runConfig(ctx context.Context, cfg Config, logger *slog.Logger, factory runtimeFactory) error {
	startup, cancelStartup := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancelStartup()
	d, err := factory.open(startup, cfg, logger)
	if err != nil {
		_ = startupCleanup(cfg, d, nil, nil)
		return errors.New("accountsvc: dependency creation failed")
	}
	app, err := factory.newApp(cfg, d, logger)
	if err != nil {
		_ = startupCleanup(cfg, d, nil, nil)
		return errors.New("accountsvc: accountkit construction failed")
	}
	// Refreshes own no database work, so always reclaim them even when HTTP drain
	// times out and shutdownRuntime must leave the library and its pools open.
	if app.closeTokens != nil {
		defer app.closeTokens()
	}
	if err = app.migrate(startup); err != nil {
		_ = startupCleanup(cfg, d, app, nil)
		return errors.New("accountsvc: migration failed")
	}
	if cfg.command == "migrate" {
		return startupCleanup(cfg, d, app, nil)
	}
	warnPlainHTTPIssuers(cfg, logger)
	ln, err := factory.listen(startup, cfg.HTTPAddr)
	if err != nil {
		_ = startupCleanup(cfg, d, app, nil)
		return errors.New("accountsvc: listener binding failed")
	}
	// 不把信号 context 直接传给审计/维护或请求：信号先触发 HTTP 排空。
	serviceCtx, cancelService := context.WithCancel(context.Background())
	defer cancelService()
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	tracker := newRequestTracker()
	state := &healthState{database: d.databasePing, redis: d.redisPing}
	handler := serviceHandler(cfg, app.endUser, app.admin, app.introspect, app.purgeFeed, app.jwks, app.identityResolve, state, app.metrics)
	server := newHTTPServer(cfg.HTTPAddr, requestMetadata(cfg.TrustedProxyCIDRs, tracker.wrap(handler)), requestCtx)
	if cfg.TLSEnabled {
		server.TLSConfig = cfg.tlsConfig.Clone()
		ln = tls.NewListener(ln, server.TLSConfig)
	}
	if startup.Err() != nil {
		_ = startupCleanup(cfg, d, app, []net.Listener{ln})
		return errors.New("accountsvc: startup timeout or cancellation")
	}
	app.start(serviceCtx)
	errorsFromServers := make(chan error, 1)
	go func() { errorsFromServers <- server.Serve(ln) }()
	if cfg.TLSEnabled {
		logger.Info("accountsvc: HTTPS listener started", "http_tls_enabled", true)
	} else {
		logger.Info("accountsvc: HTTP listener started; HTTP TLS is disabled in this process", "http_tls_enabled", false)
	}
	logEmailTemplates(cfg, logger)
	warnUnconfigured(cfg, logger)
	state.ready.Store(true)
	cancelStartup()
	stopPreload := preloadServiceTokens(app.preloadServiceTokens, cfg.Service.Issuer, logger)
	stopAdminPreload := preloadIssuerTokens(app.preloadAdminTokens, cfg.Admin.Issuer, "administrator", logger)
	var serveError error
	select {
	case <-ctx.Done():
	case <-errorsFromServers:
		serveError = errors.New("accountsvc: HTTP listener stopped unexpectedly")
	}
	stopPreload()
	stopAdminPreload()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	closeLibrary := func() {
		// Serve 可能尚未登记 listener，或因 Accept 失败先移除登记后才关闭。
		// 排空后在原关闭预算内兜底关闭自建 listener，再停止库与连接。
		_ = ln.Close()
		if app.close != nil {
			app.close()
		}
	}
	if err = shutdownRuntime(shutdown, cfg.ShutdownTimeout-15*time.Second, []*http.Server{server}, state, tracker, cancelRequests, closeLibrary, d.closeRedis, d.closePool); err != nil {
		return err
	}
	return serveError
}
