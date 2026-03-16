// Package pool manages a pool of idle executor pods using Redis.
//
// Uses a sorted set (ZSET) per template hash, where the score is the
// registration/heartbeat timestamp. Executors heartbeat by re-adding
// themselves (updating the score). Stale entries (crashed executors)
// are pruned by score before claiming.
//
// Claiming uses a Lua script that atomically prunes stale entries and
// pops the oldest idle executor in a single round-trip.
package pool

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// idleSetPrefix is the Redis key prefix for the sorted set of idle executor pods.
	// The full key is idle-executors:{templateHash}.
	idleSetPrefix = "idle-executors"

	// DefaultStaleThreshold is how long an executor can go without
	// heartbeating before it is considered stale and pruned.
	DefaultStaleThreshold = 30 * time.Second

	// redisTimeout is the context timeout for individual Redis operations.
	redisTimeout = 200 * time.Millisecond
)

// claimScript atomically prunes stale entries and pops the oldest idle executor.
// KEYS[1] = the sorted set key
// ARGV[1] = staleness threshold (unix timestamp — entries with score below this are stale)
var claimScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
local result = redis.call('ZPOPMIN', KEYS[1])
if #result > 0 then
    return result[1]
end
return nil
`)

// Pool manages executor pod assignment via Redis.
type Pool struct {
	rdb            *redis.Client
	staleThreshold time.Duration
}

// New creates a new Pool backed by the given Redis client.
func New(rdb *redis.Client) *Pool {
	return &Pool{
		rdb:            rdb,
		staleThreshold: DefaultStaleThreshold,
	}
}

// Register adds a pod to the idle set for the given template hash.
// Called by executor pods when they become idle (startup, after
// execution completes, after suspend). Also serves as heartbeat
// since it updates the score (timestamp).
func (p *Pool) Register(ctx context.Context, templateHash, podAddr string) error {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	now := float64(time.Now().Unix())
	return p.rdb.ZAdd(ctx, idleSetKey(templateHash), redis.Z{
		Score:  now,
		Member: podAddr,
	}).Err()
}

// Heartbeat re-registers the pod with the current timestamp.
// Executors should call this every staleThreshold/2.
func (p *Pool) Heartbeat(ctx context.Context, templateHash, podAddr string) error {
	return p.Register(ctx, templateHash, podAddr)
}

// Deregister removes a pod from the idle set. Called by executor pods
// on graceful shutdown while still idle.
func (p *Pool) Deregister(ctx context.Context, templateHash, podAddr string) error {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	return p.rdb.ZRem(ctx, idleSetKey(templateHash), podAddr).Err()
}

// Claim atomically prunes stale entries and pops one idle pod.
// Returns the pod address or empty string if no idle pods are available.
// This is the hot path — single Redis round-trip via Lua script.
func (p *Pool) Claim(ctx context.Context, templateHash string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()

	staleThreshold := float64(time.Now().Add(-p.staleThreshold).Unix())
	result, err := claimScript.Run(ctx, p.rdb, []string{idleSetKey(templateHash)}, staleThreshold).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	podAddr, ok := result.(string)
	if !ok {
		return "", nil
	}
	return podAddr, nil
}

// IdleCount returns the number of idle pods for a given template hash
// (including potentially stale entries). Used for monitoring.
func (p *Pool) IdleCount(ctx context.Context, templateHash string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTimeout)
	defer cancel()
	return p.rdb.ZCard(ctx, idleSetKey(templateHash)).Result()
}

func idleSetKey(templateHash string) string {
	return fmt.Sprintf("%s:%s", idleSetPrefix, templateHash)
}
