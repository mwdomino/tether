package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"

	"github.com/hashicorp/yamux"
	"github.com/mwdomino/tether/internal/proto"
)

// Config configures a Host daemon.
type Config struct {
	// Network is "unix" or "tcp".
	Network string
	// Addr is the unix socket path or "host:port".
	Addr string
	// Browser is the argv prefix; the URL is appended as the final argument.
	// If empty, DefaultBrowser() is used.
	Browser []string
	// AuthToken, if non-empty, must match the request's token.
	AuthToken string
	// Logger; defaults to slog.Default if nil.
	Logger *slog.Logger
}

// Host is the desktop-side daemon.
type Host struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	listener net.Listener
}

// New constructs a Host but does not start listening.
func New(cfg Config) (*Host, error) {
	if cfg.Network == "" {
		return nil, errors.New("host: Network required")
	}
	if cfg.Addr == "" {
		return nil, errors.New("host: Addr required")
	}
	if len(cfg.Browser) == 0 {
		cfg.Browser = DefaultBrowser()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Host{cfg: cfg, log: cfg.Logger}, nil
}

// Addr returns the active listener address, or nil if Serve has not yet bound.
func (h *Host) Addr() net.Addr {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listener == nil {
		return nil
	}
	return h.listener.Addr()
}

// Serve opens the listener and accepts connections until ctx is canceled or
// the listener returns a fatal error.
func (h *Host) Serve(ctx context.Context) error {
	if h.cfg.Network == "unix" {
		// Remove a stale socket if present.
		_ = os.Remove(h.cfg.Addr)
	}
	ln, err := net.Listen(h.cfg.Network, h.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s %s: %w", h.cfg.Network, h.cfg.Addr, err)
	}
	if h.cfg.Network == "unix" {
		_ = os.Chmod(h.cfg.Addr, 0o600)
	}
	h.mu.Lock()
	h.listener = ln
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go h.handleConn(ctx, conn)
	}
}

func (h *Host) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	session, err := yamux.Server(conn, nil)
	if err != nil {
		h.log.Error("yamux server", "err", err)
		return
	}
	defer session.Close()

	control, err := session.AcceptStream()
	if err != nil {
		h.log.Error("accept control stream", "err", err)
		return
	}
	defer control.Close()

	var req proto.Request
	if err := proto.ReadFrame(control, &req); err != nil {
		h.log.Error("read control request", "err", err)
		return
	}
	h.log.Info("request received", "url", req.URL, "loopback_ports", req.LoopbackPorts)

	if h.cfg.AuthToken != "" && req.AuthToken != h.cfg.AuthToken {
		h.log.Warn("auth token mismatch", "remote", conn.RemoteAddr())
		_ = proto.WriteFrame(control, proto.Response{OK: false, Error: "auth token mismatch"})
		return
	}

	var mgr *tunnelMgr
	if len(req.LoopbackPorts) > 0 {
		mgr = newTunnelMgr(session, h.log)
		if failed, err := mgr.bind(req.LoopbackPorts); err != nil {
			h.log.Error("bind loopback port", "port", failed, "err", err)
			_ = proto.WriteFrame(control, proto.Response{
				OK:    false,
				Error: fmt.Sprintf("port %d already in use on desktop: %s", failed, err.Error()),
			})
			return
		}
		h.log.Info("loopback ports bound", "ports", req.LoopbackPorts)
	}

	if err := h.launchBrowser(req.URL); err != nil {
		h.log.Error("browser launch", "err", err)
		if mgr != nil {
			mgr.releaseAll()
		}
		_ = proto.WriteFrame(control, proto.Response{OK: false, Error: "browser launch failed: " + err.Error()})
		return
	}
	h.log.Info("browser launched")
	if err := proto.WriteFrame(control, proto.Response{OK: true}); err != nil {
		h.log.Error("write control response", "err", err)
		if mgr != nil {
			mgr.releaseAll()
		}
		return
	}

	if mgr == nil {
		// No loopback ports → drain to EOF so client closes cleanly.
		_, _ = io.Copy(io.Discard, control)
		return
	}

	mgr.wait(ctx)
	// Closing control here signals the agent we're done.
}

func (h *Host) launchBrowser(url string) error {
	argv := append([]string{}, h.cfg.Browser...)
	argv = append(argv, url)
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child so it doesn't linger as a zombie in our long-lived daemon.
	go func() { _ = cmd.Wait() }()
	return nil
}
