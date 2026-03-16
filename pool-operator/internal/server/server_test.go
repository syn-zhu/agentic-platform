package server

import (
	"context"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
	poolpb "github.com/siyanzhu/agentic-platform/pool-operator/pkg/poolpb"
)

func setupTestServer(t *testing.T) (poolpb.PoolServiceClient, *pool.Registry) {
	t.Helper()

	registry := pool.NewRegistry()
	s := New(registry, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	grpcSrv := grpc.NewServer()
	s.RegisterGRPC(grpcSrv)
	go grpcSrv.Serve(ln)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return poolpb.NewPoolServiceClient(conn), registry
}

func TestClaim_Success(t *testing.T) {
	client, registry := setupTestServer(t)
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	resp, err := client.Claim(context.Background(), &poolpb.ClaimRequest{Pool: "test-pool"})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if resp.ClaimId == "" {
		t.Fatal("expected non-empty claim ID")
	}
	if resp.PodName != "pod-1" {
		t.Errorf("PodName = %q, want %q", resp.PodName, "pod-1")
	}
}

func TestClaim_PoolExhausted(t *testing.T) {
	client, registry := setupTestServer(t)
	registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	_, err := client.Claim(context.Background(), &poolpb.ClaimRequest{Pool: "test-pool"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestClaim_PoolNotFound(t *testing.T) {
	client, _ := setupTestServer(t)

	_, err := client.Claim(context.Background(), &poolpb.ClaimRequest{Pool: "nonexistent"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestRenew_Success(t *testing.T) {
	client, registry := setupTestServer(t)
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claimResp, _ := client.Claim(context.Background(), &poolpb.ClaimRequest{Pool: "test-pool"})

	resp, err := client.Renew(context.Background(), &poolpb.RenewRequest{ClaimId: claimResp.ClaimId})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if resp.ExpiresAt == "" {
		t.Fatal("expected non-empty expires_at")
	}
}

func TestRenew_NotFound(t *testing.T) {
	client, _ := setupTestServer(t)

	_, err := client.Renew(context.Background(), &poolpb.RenewRequest{ClaimId: "nonexistent"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestRelease_Success(t *testing.T) {
	client, registry := setupTestServer(t)
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claimResp, _ := client.Claim(context.Background(), &poolpb.ClaimRequest{Pool: "test-pool"})

	_, err := client.Release(context.Background(), &poolpb.ReleaseRequest{ClaimId: claimResp.ClaimId})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if p.Status().Available != 1 {
		t.Error("pod should be back in available pool")
	}
}

func TestRelease_NotFound(t *testing.T) {
	client, _ := setupTestServer(t)

	_, err := client.Release(context.Background(), &poolpb.ReleaseRequest{ClaimId: "nonexistent"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}
}

func TestStatus(t *testing.T) {
	client, registry := setupTestServer(t)
	registry.CreateOrUpdate("pool-a", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	resp, err := client.Status(context.Background(), &poolpb.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, ok := resp.Pools["pool-a"]; !ok {
		t.Fatal("expected pool-a in status response")
	}
}
