//go:build linux

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
	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
	"github.com/siyanzhu/agentic-platform/executor/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Event store (NoopEventStore for now — MongoDB later).
	store := lifecycle.NoopEventStore{}

	// Pod lifecycle with logging placeholder actions.
	actions := &loggingPodActions{}
	pod := lifecycle.NewPodLifecycle(actions)

	// Boot sequence: Uninitialized → Configuring → Idle.
	ctx := context.Background()
	if err := pod.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		slog.Error("config failed", "error", err)
		os.Exit(1)
	}
	if err := pod.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		slog.Error("transition to idle failed", "error", err)
		os.Exit(1)
	}
	slog.Info("pod lifecycle ready", "state", "Idle")

	// Proxy placeholder.
	proxy := &noopProxy{}

	// HTTP server.
	srv := server.New(pod, proxy, store, server.PrepareConfig{
		RunArrivalTimeout: cfg.BootTimeout,
	})

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv,
	}

	// Graceful shutdown.
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		slog.Info("executor starting", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-sigCtx.Done()
	slog.Info("shutting down")
	pod.Fire(context.Background(), lifecycle.TrigKill)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdownCtx)
}

// loggingPodActions logs each action. Placeholder until Runner implements PodActions.
type loggingPodActions struct{}

func (loggingPodActions) SetupInfra(_ context.Context) error   { slog.Info("action: SetupInfra"); return nil }
func (loggingPodActions) BootVM(_ context.Context) error        { slog.Info("action: BootVM"); return nil }
func (loggingPodActions) ResumeVM(_ context.Context) error      { slog.Info("action: ResumeVM"); return nil }
func (loggingPodActions) PauseVM(_ context.Context) error       { slog.Info("action: PauseVM"); return nil }
func (loggingPodActions) StopVM(_ context.Context)              { slog.Info("action: StopVM") }
func (loggingPodActions) CleanupWorkDir(_ context.Context)      { slog.Info("action: CleanupWorkDir") }
func (loggingPodActions) ReleaseLease(_ context.Context)        { slog.Info("action: ReleaseLease") }
func (loggingPodActions) CloseAll()                             { slog.Info("action: CloseAll") }

func (loggingPodActions) RegisterWarm(_ context.Context, sid string) error {
	slog.Info("action: RegisterWarm", "session", sid)
	return nil
}

// noopProxy is a placeholder.
type noopProxy struct{}

func (noopProxy) SetExecution(_ *lifecycle.ExecutionLifecycle) {}
