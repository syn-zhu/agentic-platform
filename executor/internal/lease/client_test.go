package lease_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	poolpb "github.com/siyanzhu/agentic-platform/executor/pkg/poolpb"
	"github.com/siyanzhu/agentic-platform/executor/internal/lease"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockPoolService implements poolpb.PoolServiceServer for testing.
type mockPoolService struct {
	poolpb.UnimplementedPoolServiceServer
	renewCount atomic.Int32
	renewErr   error
	releaseErr error
	releaseFn  func() error
}

func (m *mockPoolService) Renew(ctx context.Context, req *poolpb.RenewRequest) (*poolpb.RenewResponse, error) {
	m.renewCount.Add(1)
	if m.renewErr != nil {
		return nil, m.renewErr
	}
	return &poolpb.RenewResponse{
		ExpiresAt: time.Now().Add(30 * time.Second).Format(time.RFC3339),
	}, nil
}

func (m *mockPoolService) Release(ctx context.Context, req *poolpb.ReleaseRequest) (*poolpb.ReleaseResponse, error) {
	if m.releaseFn != nil {
		if err := m.releaseFn(); err != nil {
			return nil, err
		}
	}
	if m.releaseErr != nil {
		return nil, m.releaseErr
	}
	return &poolpb.ReleaseResponse{}, nil
}

// startTestServer starts a gRPC server on a random port and returns the address.
func startTestServer(t *testing.T, svc poolpb.PoolServiceServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	poolpb.RegisterPoolServiceServer(srv, svc)
	go srv.Serve(ln)
	t.Cleanup(srv.GracefulStop)
	return ln.Addr().String()
}

func TestRenewLoop(t *testing.T) {
	mock := &mockPoolService{}
	addr := startTestServer(t, mock)

	client, err := lease.NewClient(addr, 300*time.Millisecond) // TTL/3 = 100ms
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client.StartRenewal(ctx, "test-claim")
	time.Sleep(250 * time.Millisecond)
	cancel()

	count := mock.renewCount.Load()
	if count < 2 {
		t.Errorf("renewCount = %d, want >= 2", count)
	}
}

func TestRelease(t *testing.T) {
	var released atomic.Bool
	mock := &mockPoolService{
		releaseFn: func() error {
			released.Store(true)
			return nil
		},
	}
	addr := startTestServer(t, mock)

	client, err := lease.NewClient(addr, 30*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Release(context.Background(), "test-claim"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !released.Load() {
		t.Fatal("release was not called")
	}
}

func TestReleaseNotFoundIsSuccess(t *testing.T) {
	mock := &mockPoolService{
		releaseErr: status.Error(codes.NotFound, "claim not found"),
	}
	addr := startTestServer(t, mock)

	client, err := lease.NewClient(addr, 30*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Release(context.Background(), "test-claim"); err != nil {
		t.Fatalf("Release with NotFound should succeed: %v", err)
	}
}

func TestReleaseRetry(t *testing.T) {
	var attempts atomic.Int32
	mock := &mockPoolService{
		releaseFn: func() error {
			n := attempts.Add(1)
			if n < 3 {
				return fmt.Errorf("transient error")
			}
			return nil
		},
	}
	addr := startTestServer(t, mock)

	client, err := lease.NewClient(addr, 30*time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if err := client.Release(context.Background(), "test-claim"); err != nil {
		t.Fatalf("Release should succeed after retries: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}
