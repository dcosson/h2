package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"h2/internal/socketdir"
	"h2/internal/version"
)

const ProtocolVersion = 1

type ServerOpts struct {
	H2Dir      string
	SocketPath string
	Version    string
}

type Server struct {
	h2Dir      string
	socketPath string
	version    string
	startedAt  time.Time
}

type Request struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
}

type Response struct {
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
	Health *Health `json:"health,omitempty"`
}

type Health struct {
	PID             int    `json:"pid"`
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocol_version"`
	H2Dir           string `json:"h2_dir"`
	UptimeMillis    int64  `json:"uptime_millis"`
}

func NewServer(opts ServerOpts) *Server {
	socketPath := opts.SocketPath
	if socketPath == "" {
		socketPath = socketdir.GatewayPath()
	}
	versionString := opts.Version
	if versionString == "" {
		versionString = version.DisplayVersion()
	}
	return &Server{
		h2Dir:      opts.H2Dir,
		socketPath: socketPath,
		version:    versionString,
	}
}

func (s *Server) Run(ctx context.Context) error {
	if s.socketPath == "" {
		return fmt.Errorf("gateway socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("create gateway socket dir: %w", err)
	}
	if err := socketdir.ProbeSocket(s.socketPath, "gateway"); err != nil {
		return err
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen gateway socket: %w", err)
	}
	defer os.Remove(s.socketPath)
	defer ln.Close()

	s.startedAt = time.Now()
	go func() {
		<-ctx.Done()
		ln.Close() //nolint:errcheck
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept gateway connection: %w", err)
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	var req Request
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	if err := dec.Decode(&req); err != nil {
		writeResponse(enc, Response{OK: false, Error: fmt.Sprintf("decode request: %v", err)})
		return
	}
	if req.Version != ProtocolVersion {
		writeResponse(enc, Response{OK: false, Error: fmt.Sprintf("unsupported gateway protocol version %d; want %d", req.Version, ProtocolVersion)})
		return
	}
	switch req.Type {
	case "health":
		writeResponse(enc, Response{OK: true, Health: s.health()})
	default:
		writeResponse(enc, Response{OK: false, Error: fmt.Sprintf("unsupported gateway request type %q", req.Type)})
	}
}

func (s *Server) health() *Health {
	return &Health{
		PID:             os.Getpid(),
		Version:         s.version,
		ProtocolVersion: ProtocolVersion,
		H2Dir:           s.h2Dir,
		UptimeMillis:    time.Since(s.startedAt).Milliseconds(),
	}
}

func HealthCheck(ctx context.Context, socketPath string) (*Health, error) {
	return HealthWithVersion(ctx, socketPath, ProtocolVersion)
}

func HealthWithVersion(ctx context.Context, socketPath string, protocolVersion int) (*Health, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(Request{Version: protocolVersion, Type: "health"}); err != nil {
		return nil, fmt.Errorf("write health request: %w", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read health response: %w", err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "gateway request failed"
		}
		return nil, errors.New(resp.Error)
	}
	if resp.Health == nil {
		return nil, fmt.Errorf("gateway health response missing payload")
	}
	return resp.Health, nil
}

func writeResponse(enc *json.Encoder, resp Response) {
	_ = enc.Encode(resp)
}
