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
// (host) and the guest init process.
type Server struct {
	listener net.Listener
	ttrpc    *ttrpc.Server

	// initResponse is set before VM boot and returned when
	// the guest calls Init.
	initResponse *initpb.InitResponse

	// streamCh receives SSE chunks from the guest's Stream calls.
	// The executor reads from this channel to proxy to the HTTP response.
	streamCh chan StreamChunk

	// done is closed when the guest sends done=true on Stream.
	done chan struct{}

	closeOnce sync.Once
}

// StreamChunk is an SSE chunk received from the guest.
type StreamChunk struct {
	Data  []byte
	Done  bool
	Error string
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
		streamCh:     make(chan StreamChunk, 64),
		done:         make(chan struct{}),
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

// StreamCh returns the channel that receives SSE chunks from the guest.
func (s *Server) StreamCh() <-chan StreamChunk {
	return s.streamCh
}

// Done returns a channel that is closed when the guest signals completion.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// Close shuts down the ttrpc server and listener.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.ttrpc.Close()
		err = s.listener.Close()
		close(s.streamCh)
	})
	return err
}

// Init implements the InitControl ttrpc service.
// Called by the guest init at boot to fetch config + payload.
func (s *Server) Init(ctx context.Context, req *initpb.InitRequest) (*initpb.InitResponse, error) {
	slog.Info("guest called Init")
	return s.initResponse, nil
}

// Stream implements the InitControl ttrpc service.
// Called by the guest to push SSE chunks from the agent's response.
func (s *Server) Stream(ctx context.Context, req *initpb.StreamRequest) (*initpb.StreamResponse, error) {
	chunk := StreamChunk{
		Data:  req.Data,
		Done:  req.Done,
		Error: req.Error,
	}

	select {
	case s.streamCh <- chunk:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if req.Done {
		s.closeOnce.Do(func() {
			close(s.done)
		})
	}

	return &initpb.StreamResponse{}, nil
}
