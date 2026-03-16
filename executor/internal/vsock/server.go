package vsock

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/containerd/ttrpc"
	initpb "github.com/siyanzhu/agentic-platform/executor/internal/vsock/initpb"
)

// Server handles the vsock ttrpc protocol between the executor
// (host) and the guest init process. Only serves the Init RPC
// for config delivery — the executor talks to the agent directly
// via pasta port forwarding on localhost.
type Server struct {
	listener net.Listener
	ttrpc    *ttrpc.Server

	// initResponse is set before VM boot and returned when
	// the guest calls Init.
	initResponse *initpb.InitResponse

	closeOnce sync.Once
}

// NewServer creates a vsock ttrpc server listening on the given
// Unix socket path (Firecracker proxies vsock to this socket).
func NewServer(socketPath string, initResp *initpb.InitResponse) (*Server, error) {
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	s := &Server{
		listener:     ln,
		initResponse: initResp,
	}

	ttrpcServer, err := ttrpc.NewServer()
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("create ttrpc server: %w", err)
	}

	initpb.RegisterInitControlService(ttrpcServer, s)
	s.ttrpc = ttrpcServer

	return s, nil
}

// Serve starts accepting connections. Blocks until the listener is closed.
func (s *Server) Serve(ctx context.Context) error {
	slog.Info("vsock server serving", "addr", s.listener.Addr())
	return s.ttrpc.Serve(ctx, s.listener)
}

// Close shuts down the ttrpc server and listener.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.ttrpc.Close()
		err = s.listener.Close()
	})
	return err
}

// Init implements the InitControl ttrpc service.
// Called by the guest init at boot to fetch network config and files.
func (s *Server) Init(ctx context.Context, req *initpb.InitRequest) (*initpb.InitResponse, error) {
	slog.Info("guest called Init")
	return s.initResponse, nil
}
