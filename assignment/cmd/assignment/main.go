// Assignment service for the Magenta Agentic Platform.
//
// Manages a pool of pre-warmed executor pods via Redis. The waypoint
// calls this service (as a pre-routing hook) to get a pod assignment
// before forwarding requests to the executor.
//
// Environment variables:
//   - REDIS_ADDR: Redis address (default: redis:6379)
//   - LISTEN_ADDR: HTTP listen address (default: :8080)
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/siyanzhu/agentic-platform/assignment/internal/pool"
	"github.com/siyanzhu/agentic-platform/assignment/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	redisAddr := envOr("REDIS_ADDR", "redis:6379")
	listenAddr := envOr("LISTEN_ADDR", ":8080")

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Verify Redis connectivity.
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("failed to connect to Redis", "addr", redisAddr, "error", err)
		os.Exit(1)
	}
	logger.Info("connected to Redis", "addr", redisAddr)

	p := pool.New(rdb)
	srv := server.New(p, logger)

	httpSrv := &http.Server{
		Addr:         listenAddr,
		Handler:      srv.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("assignment service listening", "addr", listenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	httpSrv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
