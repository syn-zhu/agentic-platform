package lease

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	poolpb "github.com/siyanzhu/agentic-platform/executor/pkg/poolpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Client manages lease renewal and release with the pool operator via gRPC.
type Client struct {
	conn     *grpc.ClientConn
	pool     poolpb.PoolServiceClient
	leaseTTL time.Duration
}

// NewClient creates a gRPC lease client for the given pool operator address.
func NewClient(addr string, leaseTTL time.Duration) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial pool operator at %s: %w", addr, err)
	}

	return &Client{
		conn:     conn,
		pool:     poolpb.NewPoolServiceClient(conn),
		leaseTTL: leaseTTL,
	}, nil
}

// Close shuts down the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// StartRenewal begins a background goroutine that calls Renew
// every leaseTTL/3. Stops when ctx is cancelled.
func (c *Client) StartRenewal(ctx context.Context, claimID string) {
	interval := c.leaseTTL / 3
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := c.pool.Renew(ctx, &poolpb.RenewRequest{ClaimId: claimID})
				if err != nil {
					slog.Warn("lease renewal failed", "claim_id", claimID, "error", err)
				}
			}
		}
	}()
}

// Release notifies the pool operator that the claim is done.
// Retries up to 3 times with exponential backoff (1s, 2s, 4s).
// Treats NotFound as success (already released or expired).
func (c *Client) Release(ctx context.Context, claimID string) error {
	backoff := []time.Duration{0, 1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := range backoff {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff[attempt]):
			}
		}

		_, err := c.pool.Release(ctx, &poolpb.ReleaseRequest{ClaimId: claimID})
		if err == nil {
			return nil
		}

		// NotFound = already released, treat as success.
		if status.Code(err) == codes.NotFound {
			return nil
		}

		slog.Warn("release attempt failed", "attempt", attempt+1, "claim_id", claimID, "error", err)
	}

	return fmt.Errorf("release failed after %d attempts", len(backoff))
}
