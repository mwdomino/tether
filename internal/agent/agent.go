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
		// 15 minutes — generous headroom for slow OAuth callbacks. AWS CLI's
		// token-exchange step in particular can take several minutes when
		// the headless box reaches AWS endpoints over a slow/proxied path,
		// and a too-short timeout tears the pipe down right when the local
		// CLI is about to write its response.
		cfg.Timeout = 15 * time.Minute
	}
	if cfg.LoopbackDialer == nil {
		cfg.LoopbackDialer = func(port int) (net.Conn, error) {
			// Try IPv4 first, then IPv6 — some Python apps (e.g. AWS CLI's
			// SSO listener) bind only to ::1 when `localhost` resolves to
			// ::1 first in /etc/hosts.
			if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
				return c, nil
			}
			return net.Dial("tcp", fmt.Sprintf("[::1]:%d", port))
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

	req := proto.Request{
		URL:           cfg.URL,
		LoopbackPorts: proto.ExtractLoopbackPorts(cfg.URL),
		AuthToken:     cfg.AuthToken,
	}
	cfg.Logger.Info("agent: connected to host",
		"target", cfg.Network+" "+cfg.Addr,
		"loopback_ports", req.LoopbackPorts)

	// If we may need to accept tunnel substreams, start the accept goroutine BEFORE
	// the control round-trip. The host may open a substream as soon as the browser
	// hits the desktop-bound loopback port, which can happen before the agent reads
	// the OK response.
	var (
		doneCh    chan struct{}
		streams   chan *yamux.Stream
		acceptErr chan error
	)
	if len(req.LoopbackPorts) > 0 {
		doneCh = make(chan struct{})
		streams = make(chan *yamux.Stream)
		acceptErr = make(chan error, 1)
		go func() {
			for {
				s, err := session.AcceptStream()
				if err != nil {
					acceptErr <- err
					return
				}
				select {
				case streams <- s:
				case <-doneCh:
					_ = s.Close()
					return
				}
			}
		}()
	}

	control, err := session.OpenStream()
	if err != nil {
		if doneCh != nil {
			close(doneCh)
		}
		return fmt.Errorf("open control stream: %w", err)
	}

	if err := proto.WriteFrame(control, req); err != nil {
		if doneCh != nil {
			close(doneCh)
		}
		return fmt.Errorf("write request: %w", err)
	}

	var resp proto.Response
	if err := proto.ReadFrame(control, &resp); err != nil {
		if doneCh != nil {
			close(doneCh)
		}
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		if doneCh != nil {
			close(doneCh)
		}
		return classifyHostError(resp.Error)
	}
	cfg.Logger.Info("agent: host accepted request; browser launched on desktop")

	if len(req.LoopbackPorts) == 0 {
		cfg.Logger.Info("agent: no loopback ports; exiting")
		return nil
	}

	cfg.Logger.Info("agent: awaiting tunnel substreams from host", "ports", req.LoopbackPorts)
	return runTunnelLoop(ctx, cfg, control, doneCh, streams, acceptErr)
}

func runTunnelLoop(ctx context.Context, cfg Config, control *yamux.Stream, doneCh chan struct{}, streams chan *yamux.Stream, acceptErr chan error) error {
	defer close(doneCh)

	// Goroutine: watch control for EOF (host released all listeners).
	controlClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, control)
		close(controlClosed)
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
		cfg.Logger.Error("agent: tunnel header read", "err", err)
		return
	}
	if hdr.Kind != "tunnel" {
		cfg.Logger.Error("agent: unexpected stream kind", "kind", hdr.Kind)
		return
	}
	cfg.Logger.Info("agent: tunnel substream received; dialing local", "port", hdr.Port)
	local, err := cfg.LoopbackDialer(hdr.Port)
	if err != nil {
		cfg.Logger.Error("agent: dial loopback failed (is the tool still listening on 127.0.0.1 / ::1 ?)",
			"port", hdr.Port, "err", err)
		return
	}
	cfg.Logger.Info("agent: local dial succeeded; piping bytes", "port", hdr.Port, "local", local.RemoteAddr())
	defer local.Close()

	upN, downN := int64(0), int64(0)
	upDone := make(chan struct{})
	downDone := make(chan struct{})
	go func() {
		n, _ := io.Copy(local, s)
		upN = n
		close(upDone)
	}()
	go func() {
		n, _ := io.Copy(s, local)
		downN = n
		close(downDone)
	}()

	// Mirror the host's pattern: if the desktop→agent direction finishes
	// first (browser closed before AWS CLI responded), give AWS CLI a window
	// to write its response back. Without this, the deferred local.Close()
	// would cut AWS CLI off mid-response.
	select {
	case <-upDone:
		select {
		case <-downDone:
		case <-time.After(30 * time.Second):
		}
	case <-downDone:
		select {
		case <-upDone:
		case <-time.After(3 * time.Second):
		}
	}
	cfg.Logger.Info("agent: tunnel pipe ended",
		"port", hdr.Port, "bytes_from_desktop", upN, "bytes_from_local", downN)
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
