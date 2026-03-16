package vsock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
)

// InitConfig is the JSON config sent to the guest init over vsock.
// The guest init reads this with socat + jq.
type InitConfig struct {
	Network *NetworkConfig `json:"network,omitempty"`
	Files   []FileConfig   `json:"files,omitempty"`
}

type NetworkConfig struct {
	IP        string `json:"ip"`
	Gateway   string `json:"gateway"`
	PrefixLen int    `json:"prefix_len"`
	MTU       int    `json:"mtu"`
}

type FileConfig struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
	Mode          string `json:"mode"`
}

// NewFileConfig creates a FileConfig with base64-encoded content.
func NewFileConfig(path string, content []byte, mode string) FileConfig {
	return FileConfig{
		Path:          path,
		ContentBase64: base64.StdEncoding.EncodeToString(content),
		Mode:          mode,
	}
}

// Server listens on a Unix socket (Firecracker vsock proxy) and
// sends the init config as JSON when the guest connects.
type Server struct {
	listener net.Listener
	config   *InitConfig
	once     sync.Once
}

// NewServer creates a vsock config server on the given socket path.
func NewServer(socketPath string, config *InitConfig) (*Server, error) {
	// Remove stale socket.
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	return &Server{
		listener: ln,
		config:   config,
	}, nil
}

// Serve accepts one connection, writes the config as JSON, and closes.
// Blocks until the guest connects or the listener is closed.
func (s *Server) Serve() {
	slog.Info("vsock server waiting for guest", "addr", s.listener.Addr())

	conn, err := s.listener.Accept()
	if err != nil {
		slog.Warn("vsock accept error", "error", err)
		return
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(s.config); err != nil {
		slog.Warn("vsock write error", "error", err)
		return
	}

	slog.Info("vsock config sent to guest")
}

// Close shuts down the listener.
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		err = s.listener.Close()
	})
	return err
}
