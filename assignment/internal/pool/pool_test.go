package pool_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/siyanzhu/agentic-platform/assignment/internal/pool"
)

func setup(t *testing.T) (*pool.Pool, string, func()) {
	t.Helper()

	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	ctx := context.Background()

	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available, skipping integration test")
	}

	hash := fmt.Sprintf("test-%s", t.Name())
	p := pool.New(rdb)

	cleanup := func() {
		rdb.Del(ctx, fmt.Sprintf("idle-executors:%s", hash))
		rdb.Close()
	}

	return p, hash, cleanup
}

func TestRegisterAndClaim(t *testing.T) {
	p, hash, cleanup := setup(t)
	defer cleanup()
	ctx := context.Background()

	if err := p.Register(ctx, hash, "10.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}
	if err := p.Register(ctx, hash, "10.0.0.2:9090"); err != nil {
		t.Fatal(err)
	}

	count, err := p.IdleCount(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 idle pods, got %d", count)
	}

	podAddr, err := p.Claim(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if podAddr == "" {
		t.Fatal("expected a pod address, got empty string")
	}

	count, err = p.IdleCount(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 idle pod, got %d", count)
	}
}

func TestClaimEmptyPool(t *testing.T) {
	p, hash, cleanup := setup(t)
	defer cleanup()
	ctx := context.Background()

	podAddr, err := p.Claim(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if podAddr != "" {
		t.Fatalf("expected empty string from empty pool, got %s", podAddr)
	}
}

func TestAtomicClaimNoDuplicates(t *testing.T) {
	p, hash, cleanup := setup(t)
	defer cleanup()
	ctx := context.Background()

	if err := p.Register(ctx, hash, "10.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}

	podAddr1, err := p.Claim(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if podAddr1 != "10.0.0.1:9090" {
		t.Fatalf("expected 10.0.0.1:9090, got %s", podAddr1)
	}

	podAddr2, err := p.Claim(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if podAddr2 != "" {
		t.Fatalf("expected empty string (pod already claimed), got %s", podAddr2)
	}
}

func TestDeregister(t *testing.T) {
	p, hash, cleanup := setup(t)
	defer cleanup()
	ctx := context.Background()

	if err := p.Register(ctx, hash, "10.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}

	if err := p.Deregister(ctx, hash, "10.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}

	count, err := p.IdleCount(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 idle pods after deregister, got %d", count)
	}
}

func TestHeartbeatUpdatesScore(t *testing.T) {
	p, hash, cleanup := setup(t)
	defer cleanup()
	ctx := context.Background()

	if err := p.Register(ctx, hash, "10.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}

	// Heartbeat should not add a duplicate — just update the score.
	if err := p.Heartbeat(ctx, hash, "10.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}

	count, err := p.IdleCount(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 idle pod after heartbeat (not 2), got %d", count)
	}
}
