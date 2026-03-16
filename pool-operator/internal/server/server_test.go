// pool-operator/internal/server/server_test.go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
)

func setupTestServer() (*Server, *pool.Registry) {
	registry := pool.NewRegistry()
	s := New(registry, nil)
	return s, registry
}

func TestClaimHandler_Success(t *testing.T) {
	s, registry := setupTestServer()
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	body, _ := json.Marshal(ClaimRequest{Pool: "test-pool"})
	req := httptest.NewRequest(http.MethodPost, "/claim", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp ClaimResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ClaimID == "" {
		t.Fatal("expected non-empty claim ID")
	}
	if resp.PodName != "pod-1" {
		t.Errorf("expected pod-1, got %s", resp.PodName)
	}
}

func TestClaimHandler_PoolExhausted(t *testing.T) {
	s, registry := setupTestServer()
	registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	body, _ := json.Marshal(ClaimRequest{Pool: "test-pool"})
	req := httptest.NewRequest(http.MethodPost, "/claim", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestClaimHandler_PoolNotFound(t *testing.T) {
	s, _ := setupTestServer()

	body, _ := json.Marshal(ClaimRequest{Pool: "nonexistent"})
	req := httptest.NewRequest(http.MethodPost, "/claim", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestRenewHandler_Success(t *testing.T) {
	s, registry := setupTestServer()
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claim, _ := p.Claim()

	body, _ := json.Marshal(RenewRequest{ClaimID: claim.ClaimID})
	req := httptest.NewRequest(http.MethodPost, "/renew", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestReleaseHandler_Success(t *testing.T) {
	s, registry := setupTestServer()
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claim, _ := p.Claim()

	body, _ := json.Marshal(ReleaseRequest{ClaimID: claim.ClaimID})
	req := httptest.NewRequest(http.MethodPost, "/release", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if p.Status().Available != 1 {
		t.Error("pod should be back in available pool")
	}
}

func TestStatusHandler(t *testing.T) {
	s, registry := setupTestServer()
	registry.CreateOrUpdate("pool-a", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp.Pools["pool-a"]; !ok {
		t.Fatal("expected pool-a in status response")
	}
}
