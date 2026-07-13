package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mwdomino/tether/internal/config"
)

func TestSSHArgsIncludesForwardAndHardening(t *testing.T) {
	box := config.Box{Name: "dev", SSHHost: "dev-box", RemotePort: 9999}
	args := sshArgs(box, "/run/tether/dev.sock")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-N", "-T",
		"BatchMode=yes",
		"ExitOnForwardFailure=yes",
		"ServerAliveInterval=15",
		"-R 9999:/run/tether/dev.sock",
		"dev-box",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args missing %q; got: %s", want, joined)
		}
	}
	// ssh destination must be the final argument.
	if args[len(args)-1] != "dev-box" {
		t.Fatalf("ssh host not last arg: %v", args)
	}
}

func TestNextBackoffGrowsAndCaps(t *testing.T) {
	min, max := 1*time.Second, 8*time.Second
	got := nextBackoff(min, min, max)
	if got != 2*time.Second {
		t.Fatalf("first grow = %v, want 2s", got)
	}
	got = nextBackoff(4*time.Second, min, max)
	if got != 8*time.Second {
		t.Fatalf("grow to cap = %v, want 8s", got)
	}
	got = nextBackoff(8*time.Second, min, max)
	if got != 8*time.Second {
		t.Fatalf("cap should hold at 8s, got %v", got)
	}
}

// fakeRunner drives supervised "processes" from a behavior func indexed by
// the per-box invocation count.
type fakeRunner struct {
	mu       sync.Mutex
	calls    int
	behavior func(call int, ctx context.Context) (stderr string, err error)
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) Cmd {
	f.mu.Lock()
	call := f.calls
	f.calls++
	f.mu.Unlock()
	return &fakeCmd{ctx: ctx, call: call, behavior: f.behavior}
}

type fakeCmd struct {
	ctx      context.Context
	call     int
	behavior func(int, context.Context) (string, error)
}

func (c *fakeCmd) Start() error { return nil }
func (c *fakeCmd) Wait() (string, error) {
	return c.behavior(c.call, c.ctx)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func statusByName(s *Supervisor, name string) (BoxStatus, bool) {
	for _, st := range s.Status() {
		if st.Name == name {
			return st, true
		}
	}
	return BoxStatus{}, false
}

func TestSupervisorReachesConnected(t *testing.T) {
	fr := &fakeRunner{behavior: func(call int, ctx context.Context) (string, error) {
		<-ctx.Done() // stay "alive" until stopped
		return "", ctx.Err()
	}}
	s := New(Options{
		Runner:       fr.run,
		ConnectGrace: 20 * time.Millisecond,
		MinBackoff:   10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
		LocalSocket:  func(b config.Box) string { return "/tmp/" + b.Name + ".sock" },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, []config.Box{{Name: "dev", SSHHost: "dev-box", RemotePort: 9999}})

	waitFor(t, time.Second, func() bool {
		st, ok := statusByName(s, "dev")
		return ok && st.State == StateConnected
	})
	cancel()
	s.Wait()
}

func TestSupervisorReconnectsAndCapturesError(t *testing.T) {
	fr := &fakeRunner{behavior: func(call int, ctx context.Context) (string, error) {
		if call == 0 {
			return "Warning: remote port forwarding failed for listen port 9999", errors.New("exit status 255")
		}
		<-ctx.Done()
		return "", ctx.Err()
	}}
	s := New(Options{
		Runner:       fr.run,
		ConnectGrace: 20 * time.Millisecond,
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		LocalSocket:  func(b config.Box) string { return "/tmp/" + b.Name + ".sock" },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, []config.Box{{Name: "dev", SSHHost: "dev-box", RemotePort: 9999}})

	// It should retry (call the runner again) after the first failure.
	waitFor(t, time.Second, func() bool { return fr.count() >= 2 })
	// And it should eventually come up on the second attempt.
	waitFor(t, time.Second, func() bool {
		st, ok := statusByName(s, "dev")
		return ok && st.State == StateConnected
	})
	cancel()
	s.Wait()
}

func TestSupervisorRecordsLastErrorOnFailure(t *testing.T) {
	fr := &fakeRunner{behavior: func(call int, ctx context.Context) (string, error) {
		// Always fail fast so we can observe the recorded error.
		return "port 9999 already in use", errors.New("exit status 255")
	}}
	var (
		mu       sync.Mutex
		lastSeen string
	)
	s := New(Options{
		Runner:       fr.run,
		ConnectGrace: time.Second, // never reached; process fails immediately
		MinBackoff:   5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		LocalSocket:  func(b config.Box) string { return "/tmp/" + b.Name + ".sock" },
		OnStateChange: func(st BoxStatus) {
			if st.State == StateDisconnected && st.LastError != "" {
				mu.Lock()
				lastSeen = st.LastError
				mu.Unlock()
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, []config.Box{{Name: "dev", SSHHost: "dev-box", RemotePort: 9999}})

	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(lastSeen, "already in use")
	})
	cancel()
	s.Wait()
}

func TestSupervisorStopsOnCancel(t *testing.T) {
	fr := &fakeRunner{behavior: func(call int, ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	s := New(Options{
		Runner:      fr.run,
		LocalSocket: func(b config.Box) string { return "/tmp/" + b.Name + ".sock" },
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx, []config.Box{{Name: "dev", SSHHost: "dev-box"}})

	waitFor(t, time.Second, func() bool { return fr.count() >= 1 })
	cancel()

	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}
