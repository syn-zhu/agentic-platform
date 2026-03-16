package lease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Client manages lease renewal and release with the pool operator.
type Client struct {
	baseURL    string
	leaseTTL   time.Duration
	httpClient *http.Client
}

// NewClient creates a lease client for the given pool operator address.
func NewClient(baseURL string, leaseTTL time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		leaseTTL:   leaseTTL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// StartRenewal begins a background goroutine that renews the lease
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
				if err := c.renew(ctx, claimID); err != nil {
					slog.Warn("lease renewal failed", "claim_id", claimID, "error", err)
				}
			}
		}
	}()
}

func (c *Client) renew(ctx context.Context, claimID string) error {
	body, _ := json.Marshal(map[string]string{"claim_id": claimID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/renew", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("renew returned %d", resp.StatusCode)
	}
	return nil
}

// Release notifies the pool operator that the claim is done.
// Retries up to 3 times with exponential backoff (1s, 2s, 4s).
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

		err := c.doRelease(ctx, claimID)
		if err == nil {
			return nil
		}
		slog.Warn("release attempt failed", "attempt", attempt+1, "claim_id", claimID, "error", err)
	}

	return fmt.Errorf("release failed after %d attempts", len(backoff))
}

func (c *Client) doRelease(ctx context.Context, claimID string) error {
	body, _ := json.Marshal(map[string]string{"claim_id": claimID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/release", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil // 404 = already released, treat as success
	}
	return fmt.Errorf("release returned %d", resp.StatusCode)
}
