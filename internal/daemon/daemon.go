// Package daemon composes the host-side pieces into a single long-lived
// service: it supervises an SSH remote-forward per configured box, binds a
// per-box unix socket that the forward targets, records status and requests in
// a registry, and exposes a control socket for the status CLI and GUI. A
// reload re-reads the config and reconciles the running boxes.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mwdomino/tether/internal/config"
	"github.com/mwdomino/tether/internal/control"
	"github.com/mwdomino/tether/internal/host"
	"github.com/mwdomino/tether/internal/registry"
	"github.com/mwdomino/tether/internal/supervisor"
)

const maxRequestHistory = 200

// Options configure a Daemon. Empty fields fall back to defaults.
type Options struct {
	// ConfigPath is the box config file (required).
	ConfigPath string
	// SocketDir holds the per-box forward-target sockets.
	SocketDir string
	// ControlSocket is the client-facing IPC socket path.
	ControlSocket string
	// Browser overrides the URL-open argv (defaults to the OS opener).
	Browser []string
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// Runner is the SSH process runner; defaults to the real ssh runner.
	// Tests inject a fake.
	Runner supervisor.Runner
	// ConnectGrace is how long a supervised ssh must stay up to count as
	// connected. Zero uses the supervisor default.
	ConnectGrace time.Duration
}

// Daemon owns the registry and control server (persistent) plus the current
// "generation" of per-box listeners and SSH supervisor (rebuilt on reload).
type Daemon struct {
	opts Options
	log  *slog.Logger
	reg  *registry.Registry

	mu      sync.Mutex
	baseCtx context.Context
	gen     *generation
}

type generation struct {
	cancel context.CancelFunc
	sup    *supervisor.Supervisor
	wg     sync.WaitGroup
}

// New builds a Daemon, applying defaults.
func New(opts Options) (*Daemon, error) {
	if opts.ConfigPath == "" {
		return nil, errors.New("daemon: ConfigPath is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SocketDir == "" {
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			opts.SocketDir = filepath.Join(rt, "tether")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("daemon: resolve socket dir: %w", err)
			}
			opts.SocketDir = filepath.Join(home, ".local", "share", "tether")
		}
	}
	if opts.ControlSocket == "" {
		cs, err := config.DefaultControlSocket()
		if err != nil {
			return nil, err
		}
		opts.ControlSocket = cs
	}
	return &Daemon{
		opts: opts,
		log:  opts.Logger,
		reg:  registry.New(maxRequestHistory),
	}, nil
}

// Run starts the daemon and blocks until ctx is canceled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.opts.SocketDir, 0o700); err != nil {
		return fmt.Errorf("daemon: mkdir socket dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(d.opts.ControlSocket), 0o700); err != nil {
		return fmt.Errorf("daemon: mkdir control dir: %w", err)
	}
	if err := removeStaleSocket(d.opts.ControlSocket); err != nil {
		return err
	}
	cln, err := net.Listen("unix", d.opts.ControlSocket)
	if err != nil {
		return fmt.Errorf("daemon: listen control socket: %w", err)
	}
	_ = os.Chmod(d.opts.ControlSocket, 0o600)

	srv := control.NewServer(d.reg, d.reload, d.log)
	ctrlDone := make(chan struct{})
	go func() { _ = srv.Serve(ctx, cln); close(ctrlDone) }()

	d.mu.Lock()
	d.baseCtx = ctx
	err = d.startGenerationLocked(ctx)
	d.mu.Unlock()
	if err != nil {
		return err
	}
	d.log.Info("daemon: running", "control_socket", d.opts.ControlSocket, "socket_dir", d.opts.SocketDir)

	<-ctx.Done()

	d.mu.Lock()
	d.stopGenerationLocked()
	d.mu.Unlock()
	<-ctrlDone
	return nil
}

func (d *Daemon) reload() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.baseCtx == nil {
		return errors.New("daemon: not running")
	}
	d.stopGenerationLocked()
	d.reg.ResetBoxes()
	return d.startGenerationLocked(d.baseCtx)
}

func (d *Daemon) startGenerationLocked(parent context.Context) error {
	cfg, err := config.Load(d.opts.ConfigPath)
	if err != nil {
		return err
	}
	genCtx, cancel := context.WithCancel(parent)
	gen := &generation{cancel: cancel}

	for _, box := range cfg.Boxes {
		h, err := host.New(host.Config{
			Network:   "unix",
			Addr:      d.boxSocket(box.Name),
			Browser:   d.opts.Browser,
			AuthToken: cfg.AuthToken,
			Logger:    d.log,
			OnRequest: d.requestReporter(box.Name),
		})
		if err != nil {
			cancel()
			return err
		}
		gen.wg.Go(func() {
			if err := h.Serve(genCtx); err != nil {
				d.log.Error("daemon: box listener stopped", "box", box.Name, "err", err)
			}
		})
	}

	sup := supervisor.New(supervisor.Options{
		Runner:        d.opts.Runner,
		ConnectGrace:  d.opts.ConnectGrace,
		LocalSocket:   func(b config.Box) string { return d.boxSocket(b.Name) },
		OnStateChange: d.statusReporter(),
		Logger:        d.log,
	})
	sup.Start(genCtx, cfg.Boxes)
	gen.sup = sup

	d.gen = gen
	return nil
}

func (d *Daemon) stopGenerationLocked() {
	if d.gen == nil {
		return
	}
	d.gen.cancel()
	d.gen.sup.Wait()
	d.gen.wg.Wait()
	d.gen = nil
}

func (d *Daemon) requestReporter(box string) func(url, outcome string) {
	return func(url, outcome string) {
		d.reg.RecordRequest(registry.RequestRecord{Box: box, URL: url, Outcome: outcome})
	}
}

func (d *Daemon) statusReporter() func(supervisor.BoxStatus) {
	return func(st supervisor.BoxStatus) {
		d.reg.SetBoxStatus(registry.BoxStatus{
			Name:      st.Name,
			SSHHost:   st.SSHHost,
			State:     string(st.State),
			LastError: st.LastError,
			Since:     st.Since,
		})
	}
}

func (d *Daemon) boxSocket(name string) string {
	return filepath.Join(d.opts.SocketDir, name+".sock")
}

func removeStaleSocket(path string) error {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat socket %s: %w", path, err)
	}
	if st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("path %s exists and is not a socket", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	return nil
}
