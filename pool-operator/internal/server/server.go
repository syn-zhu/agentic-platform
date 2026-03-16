package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
	poolpb "github.com/siyanzhu/agentic-platform/pool-operator/pkg/poolpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LabelPersister handles async label updates after claim/release operations.
type LabelPersister interface {
	PersistClaimLabels(ctx context.Context, poolName, podName, claimID string, expiresAt time.Time)
	PersistReleaseLabels(ctx context.Context, poolName, podName string)
}

// Server implements the gRPC PoolService and exposes a health HTTP endpoint.
type Server struct {
	poolpb.UnimplementedPoolServiceServer
	registry  *pool.Registry
	metrics   *Metrics
	persister LabelPersister
}

// New creates a new Server backed by the given registry.
// persister may be nil (e.g., in tests); label updates will be skipped.
func New(registry *pool.Registry, persister LabelPersister) *Server {
	return &Server{
		registry:  registry,
		metrics:   NewMetrics(),
		persister: persister,
	}
}

// Metrics returns the server's Metrics instance for use by other components.
func (s *Server) Metrics() *Metrics {
	return s.metrics
}

// RegisterGRPC registers the PoolService on the given gRPC server.
func (s *Server) RegisterGRPC(srv *grpc.Server) {
	poolpb.RegisterPoolServiceServer(srv, s)
}

// HealthHandler returns an HTTP handler for /healthz (Kubernetes readiness probe).
func (s *Server) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	return mux
}

// ServeGRPC starts the gRPC server on the given listener.
func ServeGRPC(ln net.Listener, srv *grpc.Server) error {
	return srv.Serve(ln)
}

// Claim reserves an available executor pod from the named pool.
func (s *Server) Claim(ctx context.Context, req *poolpb.ClaimRequest) (*poolpb.ClaimResponse, error) {
	p := s.registry.Get(req.Pool)
	if p == nil {
		return nil, status.Errorf(codes.NotFound, "pool %q not found", req.Pool)
	}

	start := time.Now()
	claim, err := p.Claim()
	duration := time.Since(start)

	s.metrics.ClaimDuration.WithLabelValues(req.Pool).Observe(duration.Seconds())

	if err != nil {
		if errors.Is(err, pool.ErrPoolExhausted) {
			s.metrics.ExhaustedTotal.WithLabelValues(req.Pool).Inc()
			return nil, status.Errorf(codes.ResourceExhausted, "no available pods (warming: %d)", p.Status().Warming)
		}
		return nil, status.Errorf(codes.Internal, "claim failed: %v", err)
	}

	s.metrics.ClaimTotal.WithLabelValues(req.Pool).Inc()

	if s.persister != nil {
		go s.persister.PersistClaimLabels(ctx, req.Pool, claim.PodInfo.Name, claim.ClaimID, claim.ExpiresAt)
	}

	return &poolpb.ClaimResponse{
		PodName: claim.PodInfo.Name,
		PodIp:   claim.PodInfo.IP,
		PodPort: claim.PodInfo.Port,
		ClaimId: claim.ClaimID,
	}, nil
}

// Renew extends the lease on a claimed pod.
func (s *Server) Renew(ctx context.Context, req *poolpb.RenewRequest) (*poolpb.RenewResponse, error) {
	for _, p := range s.registry.List() {
		expiresAt, err := p.Renew(req.ClaimId)
		if err == nil {
			return &poolpb.RenewResponse{
				ExpiresAt: expiresAt.Format(time.RFC3339),
			}, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "claim %q not found", req.ClaimId)
}

// Release returns a claimed pod to the available pool.
func (s *Server) Release(ctx context.Context, req *poolpb.ReleaseRequest) (*poolpb.ReleaseResponse, error) {
	for _, p := range s.registry.List() {
		podInfo, err := p.Release(req.ClaimId)
		if err == nil {
			if s.persister != nil {
				go s.persister.PersistReleaseLabels(ctx, p.Name(), podInfo.Name)
			}
			return &poolpb.ReleaseResponse{}, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "claim %q not found", req.ClaimId)
}

// Status returns pool metrics for all pools.
func (s *Server) Status(ctx context.Context, req *poolpb.StatusRequest) (*poolpb.StatusResponse, error) {
	pools := s.registry.List()
	resp := &poolpb.StatusResponse{
		Pools: make(map[string]*poolpb.PoolStatus, len(pools)),
	}
	for _, p := range pools {
		st := p.Status()
		resp.Pools[p.Name()] = &poolpb.PoolStatus{
			Desired:   int32(st.Desired),
			Available: int32(st.Available),
			Claimed:   int32(st.Claimed),
			Warming:   int32(st.Warming),
		}
	}
	return resp, nil
}
