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

	// eventCh receives SSE events from the guest's EmitEvent calls.
	// The executor reads from this channel to proxy to the HTTP response.
	eventCh chan Event

	// done is closed when the guest sends done=true on EmitEvent.
	done chan struct{}

	closeOnce sync.Once
}

// Event is an SSE event received from the guest.
type Event struct {
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
		eventCh:      make(chan Event, 64),
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

// EventCh returns the channel that receives SSE events from the guest.
func (s *Server) EventCh() <-chan Event {
	return s.eventCh
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
		close(s.eventCh)
	})
	return err
}

// Init implements the InitControl ttrpc service.
// Called by the guest init at boot to fetch config + payload.
func (s *Server) Init(ctx context.Context, req *initpb.InitRequest) (*initpb.InitResponse, error) {
	slog.Info("guest called Init")
	return s.initResponse, nil
}

// EmitEvent implements the InitControl ttrpc service.
// Called by the guest to push SSE events from the agent's response.
func (s *Server) EmitEvent(ctx context.Context, req *initpb.EmitEventRequest) (*initpb.EmitEventResponse, error) {
	ev := Event{
		Data:  req.Data,
		Done:  req.Done,
		Error: req.Error,
	}

	select {
	case s.eventCh <- ev:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if req.Done {
		s.closeOnce.Do(func() {
			close(s.done)
		})
	}

	return &initpb.EmitEventResponse{}, nil
}
