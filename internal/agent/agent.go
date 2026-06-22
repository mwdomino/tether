package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/mwdomino/tether/internal/proto"
)

var (
	ErrHostUnreachable = errors.New("tether: host unreachable")
	ErrPortCollision   = errors.New("tether: desktop port already in use")
	ErrAuthMismatch    = errors.New("tether: auth token mismatch")
	ErrTimeout         = errors.New("tether: timeout waiting for callback")
	ErrBrowserLaunch   = errors.New("tether: browser launch failed")
)

// Config is one invocation's input.
type Config struct {
	Network        string
	Addr           string
	URL            string
	AuthToken      string
	Timeout        time.Duration
	Logger         *slog.Logger
	LoopbackDialer func(port int) (net.Conn, error) // nil → 127.0.0.1:<port>
}

// Run performs one open: connects to host, sends request, optionally relays
// loopback callback streams, returns when done or on error.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.LoopbackDialer == nil {
		cfg.LoopbackDialer = func(port int) (net.Conn, error) {
			return net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		}
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, cfg.Network, cfg.Addr)
	if err != nil {
		return ErrHostUnreachable
	}
	defer conn.Close()

	session, err := yamux.Client(conn, nil)
	if err != nil {
		return fmt.Errorf("yamux client: %w", err)
	}
	defer session.Close()

	control, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}

	req := proto.Request{
		URL:           cfg.URL,
		LoopbackPorts: proto.ExtractLoopbackPorts(cfg.URL),
		AuthToken:     cfg.AuthToken,
	}
	if err := proto.WriteFrame(control, req); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	var resp proto.Response
	if err := proto.ReadFrame(control, &resp); err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return classifyHostError(resp.Error)
	}

	if len(req.LoopbackPorts) == 0 {
		return nil
	}

	// Tunnel mode: accept substreams until control closes or timeout.
	return runTunnelLoop(ctx, cfg, session, control)
}

func runTunnelLoop(ctx context.Context, cfg Config, session *yamux.Session, control *yamux.Stream) error {
	// Goroutine: watch control for EOF (host released all listeners).
	controlClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, control)
		close(controlClosed)
	}()

	streams := make(chan *yamux.Stream)
	acceptErr := make(chan error, 1)
	go func() {
		for {
			s, err := session.AcceptStream()
			if err != nil {
				acceptErr <- err
				return
			}
			streams <- s
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ErrTimeout
		case <-controlClosed:
			return nil
		case <-acceptErr:
			return nil // session closed, normal completion
		case s := <-streams:
			go handleTunnel(cfg, s)
		}
	}
}

func handleTunnel(cfg Config, s *yamux.Stream) {
	defer s.Close()
	var hdr proto.TunnelHeader
	if err := proto.ReadFrame(s, &hdr); err != nil {
		cfg.Logger.Error("tunnel header read", "err", err)
		return
	}
	if hdr.Kind != "tunnel" {
		cfg.Logger.Error("unexpected stream kind", "kind", hdr.Kind)
		return
	}
	local, err := cfg.LoopbackDialer(hdr.Port)
	if err != nil {
		cfg.Logger.Error("dial loopback", "port", hdr.Port, "err", err)
		return
	}
	defer local.Close()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(local, s); done <- struct{}{} }()
	go func() { _, _ = io.Copy(s, local); done <- struct{}{} }()
	<-done
}

func classifyHostError(msg string) error {
	switch {
	case msg == "auth token mismatch":
		return ErrAuthMismatch
	case strings.HasPrefix(msg, "port "):
		return fmt.Errorf("%w: %s", ErrPortCollision, msg)
	case strings.HasPrefix(msg, "browser launch"):
		return fmt.Errorf("%w: %s", ErrBrowserLaunch, msg)
	default:
		return fmt.Errorf("host: %s", msg)
	}
}
