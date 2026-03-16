package lease_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siyanzhu/agentic-platform/executor/internal/lease"
)

func TestRenewLoop(t *testing.T) {
	var renewCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/renew" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			ClaimID string `json:"claim_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.ClaimID != "test-claim" {
			t.Errorf("claim_id = %q, want %q", body.ClaimID, "test-claim")
		}
		renewCount.Add(1)
		json.NewEncoder(w).Encode(map[string]string{
			"expires_at": time.Now().Add(30 * time.Second).Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	client := lease.NewClient(srv.URL, 300*time.Millisecond) // short TTL for test
	ctx, cancel := context.WithCancel(context.Background())

	client.StartRenewal(ctx, "test-claim")
	time.Sleep(250 * time.Millisecond) // wait for at least 2 renewals (interval = TTL/3 = 100ms)
	cancel()

	count := renewCount.Load()
	if count < 2 {
		t.Errorf("renewCount = %d, want >= 2", count)
	}
}

func TestRelease(t *testing.T) {
	var released atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release" && r.Method == http.MethodPost {
			released.Store(true)
			json.NewEncoder(w).Encode(map[string]string{"status": "released"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := lease.NewClient(srv.URL, 30*time.Second)
	err := client.Release(context.Background(), "test-claim")
	if err != nil {
		t.Fatalf("Release() error: %v", err)
	}
	if !released.Load() {
		t.Fatal("release endpoint was not called")
	}
}

func TestRelease404IsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := lease.NewClient(srv.URL, 30*time.Second)
	err := client.Release(context.Background(), "test-claim")
	if err != nil {
		t.Fatalf("Release() with 404 should succeed: %v", err)
	}
}

func TestReleaseRetry(t *testing.T) {
	var attempts atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "released"})
	}))
	defer srv.Close()

	client := lease.NewClient(srv.URL, 30*time.Second)
	err := client.Release(context.Background(), "test-claim")
	if err != nil {
		t.Fatalf("Release() should succeed after retries: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}
