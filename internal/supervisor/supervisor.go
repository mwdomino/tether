// Package supervisor keeps a managed SSH remote-forward alive for each
// configured box. It owns the ssh(1) process directly (rather than relying on
// the user's interactive session), so forwards survive laptop sleep and
// reconnect automatically — and, crucially, fail loudly instead of silently.
package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mwdomino/tether/internal/config"
)

// State is a box's current connection state.
type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateDisconnected State = "disconnected"
)

// BoxStatus is a snapshot of one box's supervised connection.
type BoxStatus struct {
	Name      string
	SSHHost   string
	State     State
	LastError string
	Since     time.Time
}

// Cmd is the minimal process handle the supervisor drives. It is satisfied by
// a real ssh process and by fakes in tests.
type Cmd interface {
	Start() error
	// Wait blocks until the process exits, returning captured stderr and the
	// exit error (nil on a clean exit).
	Wait() (stderr string, err error)
}

// Runner starts a command bound to ctx (canceling ctx must terminate it).
type Runner func(ctx context.Context, name string, args ...string) Cmd

// Options configure a Supervisor. Zero values fall back to sensible defaults.
type Options struct {
	Runner        Runner
	ConnectGrace  time.Duration
	MinBackoff    time.Duration
	MaxBackoff    time.Duration
	LocalSocket   func(config.Box) string
	OnStateChange func(BoxStatus)
	Logger        *slog.Logger
}

// Supervisor manages one goroutine per box.
type Supervisor struct {
	opts Options
	log  *slog.Logger

	mu     sync.Mutex
	status map[string]BoxStatus
	order  []string

	wg sync.WaitGroup
}

// New builds a Supervisor, applying defaults.
func New(opts Options) *Supervisor {
	if opts.Runner == nil {
		opts.Runner = ExecRunner
	}
	if opts.ConnectGrace <= 0 {
		opts.ConnectGrace = 3 * time.Second
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = 1 * time.Second
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 30 * time.Second
	}
	if opts.LocalSocket == nil {
		opts.LocalSocket = func(b config.Box) string { return b.Name + ".sock" }
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Supervisor{
		opts:   opts,
		log:    opts.Logger,
		status: make(map[string]BoxStatus),
	}
}

// Start launches a supervision goroutine per box. It returns immediately;
// call Wait to block until all goroutines stop after ctx is canceled.
func (s *Supervisor) Start(ctx context.Context, boxes []config.Box) {
	for _, box := range boxes {
		s.setState(box, StateConnecting, "")
		s.wg.Add(1)
		go func(b config.Box) {
			defer s.wg.Done()
			s.runBox(ctx, b)
		}(box)
	}
}

// Wait blocks until all supervision goroutines have exited.
func (s *Supervisor) Wait() { s.wg.Wait() }

// Status returns a snapshot of every box's state, in insertion order.
func (s *Supervisor) Status() []BoxStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BoxStatus, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.status[name])
	}
	return out
}

func (s *Supervisor) runBox(ctx context.Context, box config.Box) {
	backoff := s.opts.MinBackoff
	for {
		if ctx.Err() != nil {
			s.setState(box, StateDisconnected, "")
			return
		}
		s.setState(box, StateConnecting, "")

		cmd := s.opts.Runner(ctx, "ssh", sshArgs(box, s.opts.LocalSocket(box))...)
		started := time.Now()
		if err := cmd.Start(); err != nil {
			s.setState(box, StateDisconnected, err.Error())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.opts.MinBackoff, s.opts.MaxBackoff)
			continue
		}

		// Promote to Connected once the process has stayed up past the grace
		// window. Stopped if the process exits sooner.
		graceTimer := time.AfterFunc(s.opts.ConnectGrace, func() {
			if ctx.Err() == nil {
				s.setState(box, StateConnected, "")
			}
		})

		stderr, waitErr := cmd.Wait()
		graceTimer.Stop()

		if ctx.Err() != nil {
			s.setState(box, StateDisconnected, "")
			return
		}

		msg := strings.TrimSpace(stderr)
		if msg == "" && waitErr != nil {
			msg = waitErr.Error()
		}
		s.setState(box, StateDisconnected, msg)
		s.log.Warn("supervisor: ssh exited", "box", box.Name, "err", msg)

		// A connection that stayed up past the grace window is treated as
		// healthy, so a later drop restarts from the minimum backoff.
		if time.Since(started) >= s.opts.ConnectGrace {
			backoff = s.opts.MinBackoff
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, s.opts.MinBackoff, s.opts.MaxBackoff)
	}
}

func (s *Supervisor) setState(box config.Box, state State, lastErr string) {
	s.mu.Lock()
	prev, existed := s.status[box.Name]
	if !existed {
		s.order = append(s.order, box.Name)
	}
	changed := !existed || prev.State != state || prev.LastError != lastErr
	st := BoxStatus{
		Name:      box.Name,
		SSHHost:   box.SSHHost,
		State:     state,
		LastError: lastErr,
		Since:     prev.Since,
	}
	if !existed || prev.State != state {
		st.Since = time.Now()
	}
	s.status[box.Name] = st
	s.mu.Unlock()

	if changed && s.opts.OnStateChange != nil {
		s.opts.OnStateChange(st)
	}
}

// sshArgs builds the argv for a managed remote-forward. localTarget is the
// host-side endpoint (a unix socket path) that the box's remote port forwards
// to.
func sshArgs(box config.Box, localTarget string) []string {
	return []string{
		"-N", "-T",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-R", fmt.Sprintf("%d:%s", box.RemotePort, localTarget),
		box.SSHHost,
	}
}

// nextBackoff doubles cur, clamped to [min, max].
func nextBackoff(cur, min, max time.Duration) time.Duration {
	next := cur * 2
	if next < min {
		next = min
	}
	if next > max {
		next = max
	}
	return next
}

// sleepCtx sleeps for d or until ctx is canceled. It returns false if ctx was
// canceled (caller should stop).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ExecRunner is the production Runner: a real ssh process whose stderr is
// captured and whose lifetime is bound to ctx.
func ExecRunner(ctx context.Context, name string, args ...string) Cmd {
	c := exec.CommandContext(ctx, name, args...)
	return &execCmd{cmd: c}
}

type execCmd struct {
	cmd    *exec.Cmd
	stderr bytes.Buffer
}

func (e *execCmd) Start() error {
	e.cmd.Stderr = &e.stderr
	return e.cmd.Start()
}

func (e *execCmd) Wait() (string, error) {
	err := e.cmd.Wait()
	return e.stderr.String(), err
}
