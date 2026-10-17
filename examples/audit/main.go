// 三种审计装配模式的本地开发示例；运行配置沿用 examples/embedded。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// assembleLocal 由库创建、启动和关闭本地审计写侧，并配置本地读侧。
func assembleLocal(cfg accountkit.Config, deps accountkit.Deps) (*accountkit.Auth, error) {
	return accountkit.New(cfg, deps)
}

// assembleHostReadWrite 复用配置中的 source/namespace；组件的生命周期归宿主。
// 真实中心审计可提供自己的 Recorder/Reader，不能直接在 Record 中阻塞网络调用。
func assembleHostReadWrite(cfg accountkit.Config, deps accountkit.Deps, writer audit.Recorder, reader audit.Reader) (*accountkit.Auth, error) {
	deps.Audit, deps.AuditReader = writer, reader
	return accountkit.New(cfg, deps)
}

// assembleHostWriteOnly 只接管写入；已授权管理审计查询返回
// 503 AUDIT_QUERY_NOT_CONFIGURED，其他认证与管理能力照常工作。
func assembleHostWriteOnly(cfg accountkit.Config, deps accountkit.Deps, writer audit.Recorder) (*accountkit.Auth, error) {
	deps.Audit, deps.AuditReader = writer, nil
	return accountkit.New(cfg, deps)
}

func run() error {
	if os.Getenv("ACCOUNTKIT_DEMO") != "1" {
		return fmt.Errorf("set ACCOUNTKIT_DEMO=1 for this local-only example with log senders")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	cfg, err := accountkit.ConfigFromEnv("ACCOUNTKIT_")
	if err != nil {
		return err
	}
	dsn, addr := os.Getenv("ACCOUNTKIT_DATABASE_URL"), os.Getenv("ACCOUNTKIT_REDIS_ADDR")
	if dsn == "" || addr == "" {
		return fmt.Errorf("ACCOUNTKIT_DATABASE_URL and ACCOUNTKIT_REDIS_ADDR are required")
	}
	pc, err := accountkit.PoolConfig(dsn, cfg.Schema)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	deps := accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: sender.NewLog(nil), EmailSender: sender.NewLog(nil)}
	var auth *accountkit.Auth
	var owned *audit.Async
	switch mode := os.Getenv("ACCOUNTKIT_AUDIT_MODE"); mode {
	case "", "local":
		auth, err = assembleLocal(cfg, deps)
	case "host", "write-only":
		// 两个值必须由宿主明确配置。相同配置同时传给认证生产方和存储适配器。
		// 本示例仍使用同库 Store，只演示宿主接管组件，并非跨库中心审计实现。
		store, storeErr := audit.NewStoreWithOptions(pool, audit.StoreOptions{Source: cfg.AuditSource, Namespace: cfg.AuditNamespace})
		if storeErr != nil {
			return storeErr
		}
		owned = audit.NewAsync(store, audit.AsyncOptions{})
		// 先注册宿主关闭；后注册的 auth.Close 会先停止库维护任务。
		defer owned.Close()
		if mode == "host" {
			auth, err = assembleHostReadWrite(cfg, deps, owned, store)
		} else {
			auth, err = assembleHostWriteOnly(cfg, deps, owned)
		}
	default:
		return fmt.Errorf("ACCOUNTKIT_AUDIT_MODE must be local, host or write-only")
	}
	if err != nil {
		return err
	}
	defer auth.Close()
	if err := auth.Migrate(ctx); err != nil {
		return err
	}
	if owned != nil {
		// 宿主通过 Close 收尾，信号取消不会先于库维护任务停止此 worker。
		owned.Start(context.Background())
	}
	auth.Start(ctx)
	router := chi.NewRouter()
	router.Mount("/v1", auth.EndUserHandler())
	// 实际管理查询还需宿主注入 AdminVerifier/AdminPrincipal；本示例未配置。
	router.Mount("/admin/v1", auth.AdminHandler())
	server := &http.Server{Addr: "127.0.0.1:8080", Handler: router, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		// 此简化开发示例的 Shutdown 超时后不保证仍在执行的 handler 已结束；
		// 返回时 defer 会关闭认证、审计和连接池。生产宿主须先完成请求排空，
		// 或在总关闭预算耗尽时终止进程，不能把这里的超时当作安全排空完成。
		return server.Shutdown(shutdown)
	}
}

func main() {
	if err := run(); err != nil {
		slog.Error("audit example host stopped", "error", err)
		os.Exit(1)
	}
}
