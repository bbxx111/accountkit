package accountsvc

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
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
	migrate   func(context.Context) error
	initAdmin func(context.Context) error
	// preloadServiceTokens 在监听前尝试加载服务令牌签发方的密钥；失败只记录警告。
	preloadServiceTokens       func(context.Context) error
	start                      func(context.Context)
	close                      func()
	endUser, admin, introspect http.Handler
	purgeFeed                  http.Handler
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
	return "", errors.New("accountsvc: command must be serve or migrate")
}

// Run 接收主入口的信号 context，执行安全配置校验与完整服务生命周期。
func Run(ctx context.Context, args []string, logger *slog.Logger) error {
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

// Admin 路由在 New 时装配角色契约；远程 discovery 在 Migrate 之后完成。
// 只有初始化成功后才监听，因此 verifier 的赋值先于所有 HTTP 访问。
type adminBridge struct{ verifier *adminauth.Verifier }

func (b *adminBridge) RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if b.verifier == nil {
				apierror.Write(w, apierror.New(apierror.StatusUnavailable, "ADMIN_NOT_CONFIGURED", "administrator authentication is not configured"))
				return
			}
			b.verifier.RequireRole(role)(next).ServeHTTP(w, r)
		})
	}
}

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
		s, err := smtp.NewWithRenderer(cfg.SMTP, smtp.LegacyChineseRenderer{Brand: cfg.SMTPBrand})
		if err != nil {
			return nil, errors.New("accountsvc: SMTP configuration failed")
		}
		email = s
	}
	// 服务使用默认本地审计读写；Auth 拥有审计 worker，服务拥有 Pool/Redis。
	// 外部审计装配属于嵌入宿主扩展，见 examples/audit，不在此创建远端适配器。
	deps := accountkit.Deps{Pool: d.pool, Redis: d.redis, SMSSender: sms, EmailSender: email, Logger: logger, ClientIP: clientIP, RequestID: requestID, Observer: observer}
	bridge := &adminBridge{}
	if cfg.command == "serve" && cfg.AdminEnabled {
		deps.AdminVerifier = bridge
		deps.AdminPrincipal = adminauth.PrincipalFrom
	}
	auth, err := accountkit.New(cfg.Library, deps)
	if err != nil {
		return nil, errors.New("accountsvc: accountkit construction failed")
	}
	app := &application{migrate: auth.Migrate, start: auth.Start, close: auth.Close, endUser: auth.EndUserHandler(), admin: auth.AdminHandler(), jwks: auth.JWKSHandler(), metrics: metrics}
	if cfg.command == "serve" {
		intro, purge, preload, err := serviceAuthenticators(cfg, logger, nil)
		if err != nil {
			auth.Close()
			return nil, err
		}
		app.preloadServiceTokens = preload
		app.introspect, err = introspection.NewWithAuthenticator(intro, auth.Users().AuthenticateStrict)
		if err != nil {
			auth.Close()
			return nil, errors.New("accountsvc: introspection configuration failed")
		}
		app.purgeFeed, err = purgefeed.NewWithAuthenticator(purge, auth.Users().ListPurgedUsers)
		if err != nil {
			auth.Close()
			return nil, errors.New("accountsvc: purged user feed configuration failed")
		}
	}
	app.initAdmin = func(ctx context.Context) error {
		if !cfg.AdminEnabled {
			return nil
		}
		verifier, err := adminauth.New(ctx, cfg.Admin, adminauth.Options{OnForbidden: auth.RecordAdminForbidden})
		if err != nil {
			return errors.New("accountsvc: administrator discovery failed")
		}
		bridge.verifier = verifier
		app.admin = verifier.Middleware(auth.AdminHandler())
		return nil
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

// serviceAuthenticators 为内省与已清理用户拉取构造调用认证。两个接口共享一个服务令牌
// verifier（延迟加载）；preload 为 nil 表示未启用 Bearer。client 仅供测试注入。
func serviceAuthenticators(cfg Config, logger *slog.Logger, client *http.Client) (intro, purge *serviceauth.Authenticator, preload func(context.Context) error, err error) {
	var tokens *oidcjwt.Verifier
	if cfg.ServiceToken.Enabled() {
		tokens, err = oidcjwt.New(cfg.ServiceToken.Issuer, cfg.ServiceToken.Audience, oidcjwt.Options{HTTPClient: client, RefreshInterval: serviceTokenRefreshInterval, RefreshTimeout: serviceTokenRefreshTimeout, Logger: logger})
		if err != nil {
			return nil, nil, nil, errors.New("accountsvc: service token configuration failed")
		}
		preload = tokens.Load
	}
	build := func(realm, endpoint string, basic map[string][]string, clients []string) (*serviceauth.Authenticator, error) {
		c := serviceauth.Config{Realm: realm, Endpoint: endpoint, Logger: logger}
		if len(basic) > 0 {
			c.Basic = basic
		}
		if len(clients) > 0 {
			c.TokenClients, c.Tokens = clients, tokens
		}
		return serviceauth.New(c)
	}
	if intro, err = build(introspection.Realm, "introspect", cfg.IntrospectionClients, cfg.ServiceToken.IntrospectionClients); err != nil {
		return nil, nil, nil, errors.New("accountsvc: introspection configuration failed")
	}
	if purge, err = build(purgefeed.Realm, "purged_users", cfg.PurgeFeedClients, cfg.ServiceToken.PurgeFeedClients); err != nil {
		return nil, nil, nil, errors.New("accountsvc: purged user feed configuration failed")
	}
	return intro, purge, preload, nil
}

// preloadServiceTokens 在就绪后于后台尝试加载服务令牌签发方的密钥，返回的函数取消并等待它。
// 签发方故障（含挂起）既不消耗启动预算也不推迟就绪：Bearer 调用在恢复前返回 503，消费者路由与
// Basic 调用照常可用。
func preloadServiceTokens(preload func(context.Context) error, issuer string, logger *slog.Logger) (stop func()) {
	if preload == nil {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), serviceTokenPreloadTimeout)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := preload(ctx); err != nil && !errors.Is(err, context.Canceled) {
			if errors.Is(err, oidcjwt.ErrIssuerMismatch) {
				logger.Error("accountsvc: service token issuer discovery does not match ACCOUNTSVC_SERVICE_ISSUER", "issuer", issuer)
				return
			}
			logger.Warn("accountsvc: service token issuer unavailable at startup; bearer calls answer 503 until it recovers", "issuer", issuer)
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
	if err = app.migrate(startup); err != nil {
		_ = startupCleanup(cfg, d, app, nil)
		return errors.New("accountsvc: migration failed")
	}
	if cfg.command == "migrate" {
		return startupCleanup(cfg, d, app, nil)
	}
	if cfg.AdminEnabled {
		if err = app.initAdmin(startup); err != nil {
			_ = startupCleanup(cfg, d, app, nil)
			return errors.New("accountsvc: administrator initialization failed")
		}
	}
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
	handler := serviceHandler(cfg, app.endUser, app.admin, app.introspect, app.purgeFeed, app.jwks, state, app.metrics)
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
	state.ready.Store(true)
	cancelStartup()
	stopPreload := preloadServiceTokens(app.preloadServiceTokens, cfg.ServiceToken.Issuer, logger)
	var serveError error
	select {
	case <-ctx.Done():
	case <-errorsFromServers:
		serveError = errors.New("accountsvc: HTTP listener stopped unexpectedly")
	}
	stopPreload()
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
