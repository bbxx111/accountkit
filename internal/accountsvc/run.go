package accountsvc

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
	"github.com/bbxx111/accountkit/internal/accountsvc/introspection"
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
	migrate                     func(context.Context) error
	initAdmin                   func(context.Context) error
	start                       func(context.Context)
	close                       func()
	consumer, admin, introspect http.Handler
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
		return nil, invalidConfig("ACCOUNTSVC_DATABASE_URL / ACCOUNTKIT_AUTH_SCHEMA")
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
	var email sender.EmailSender = sender.Disabled{}
	if cfg.command == "serve" {
		s, err := smtp.New(cfg.SMTP)
		if err != nil {
			return nil, errors.New("accountsvc: SMTP configuration failed")
		}
		email = s
	}
	deps := accountkit.Deps{Pool: d.pool, Redis: d.redis, SMSSender: sender.Disabled{}, EmailSender: email, Logger: logger, ClientIP: clientIP, RequestID: requestID}
	bridge := &adminBridge{}
	if cfg.command == "serve" && cfg.AdminEnabled {
		deps.AdminVerifier = bridge
		deps.AdminPrincipal = adminauth.PrincipalFrom
	}
	auth, err := accountkit.New(cfg.Library, deps)
	if err != nil {
		return nil, errors.New("accountsvc: accountkit construction failed")
	}
	app := &application{migrate: auth.Migrate, start: auth.Start, close: auth.Close, consumer: auth.ConsumerHandler(), admin: auth.AdminHandler()}
	if cfg.command == "serve" {
		app.introspect, err = introspection.New(cfg.IntrospectionClients, auth.Users().Authenticate)
		if err != nil {
			auth.Close()
			return nil, errors.New("accountsvc: introspection configuration failed")
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
	handler := serviceHandler(cfg, app.consumer, app.admin, app.introspect, state)
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
	var serveError error
	select {
	case <-ctx.Done():
	case <-errorsFromServers:
		serveError = errors.New("accountsvc: HTTP listener stopped unexpectedly")
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err = shutdownRuntime(shutdown, cfg.ShutdownTimeout-15*time.Second, []*http.Server{server}, state, tracker, cancelRequests, app.close, d.closeRedis, d.closePool); err != nil {
		return err
	}
	return serveError
}
