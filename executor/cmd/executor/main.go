package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/siyanzhu/agentic-platform/executor/internal/config"
	"github.com/siyanzhu/agentic-platform/executor/internal/executor"
	"github.com/siyanzhu/agentic-platform/executor/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	sm := executor.NewStateMachine()

	// Runner will be wired in Phase 4 (VM lifecycle).
	// For now, /run returns 500 "no runner configured".
	srv := server.New(sm, nil)

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv,
	}

	// Graceful shutdown on SIGTERM/SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		slog.Info("executor starting", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdownCtx)
}
