// accountsvc 是 accountkit 的可选独立宿主。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bbxx111/accountkit/internal/accountsvc"
)

func runMain(ctx context.Context, args []string, logger *slog.Logger) int {
	if err := accountsvc.Run(ctx, args, logger); err != nil {
		logger.Error("accountsvc stopped", "error", err)
		return 1
	}
	return 0
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runMain(ctx, os.Args[1:], logger)
	stop()
	if code != 0 {
		os.Exit(code)
	}
}
