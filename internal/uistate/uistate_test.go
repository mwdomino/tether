package uistate

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mwdomino/tether/internal/control"
	"github.com/mwdomino/tether/internal/registry"
)

func TestAggregate(t *testing.T) {
	cases := []struct {
		name string
		view View
		want Health
	}{
		{"daemon down", View{Connected: false}, HealthDaemonDown},
		{"no boxes", View{Connected: true}, HealthEmpty},
		{"all connected", View{Connected: true, Boxes: []registry.BoxStatus{{State: "connected"}, {State: "connected"}}}, HealthOK},
		{"one connecting", View{Connected: true, Boxes: []registry.BoxStatus{{State: "connected"}, {State: "connecting"}}}, HealthDegraded},
		{"one down", View{Connected: true, Boxes: []registry.BoxStatus{{State: "connected"}, {State: "disconnected"}}}, HealthDown},
	}
	for _, tc := range cases {
		if got := Aggregate(tc.view); got != tc.want {
			t.Errorf("%s: Aggregate = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func startServer(t *testing.T, reg *registry.Registry, onReload func() error) (sock string, stop func()) {
	t.Helper()
	sock = filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket here: %v", err)
	}
	srv := control.NewServer(reg, onReload, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, ln); close(done) }()
	return sock, func() { cancel(); _ = ln.Close(); <-done }
}

type collector struct {
	mu    sync.Mutex
	views []View
}

func (c *collector) onUpdate(v View) {
	c.mu.Lock()
	c.views = append(c.views, v)
	c.mu.Unlock()
}

func (c *collector) last() (View, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.views) == 0 {
		return View{}, false
	}
	return c.views[len(c.views)-1], true
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestModelReceivesSnapshotAndLiveEvents(t *testing.T) {
	reg := registry.New(50)
	reg.SetBoxStatus(registry.BoxStatus{Name: "dev", State: "connected"})
	sock, stop := startServer(t, reg, nil)
	defer stop()

	c := &collector{}
	m := NewModel(Options{Network: "unix", Addr: sock, Retry: 20 * time.Millisecond, OnUpdate: c.onUpdate})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	// Initial snapshot: connected to daemon, one box.
	waitFor(t, 2*time.Second, func() bool {
		v, ok := c.last()
		return ok && v.Connected && len(v.Boxes) == 1 && v.Boxes[0].Name == "dev"
	})

	// A live request event flows through.
	reg.RecordRequest(registry.RequestRecord{Box: "dev", URL: "http://x/1", Outcome: "launched"})
	waitFor(t, 2*time.Second, func() bool {
		v, ok := c.last()
		return ok && len(v.Requests) == 1 && v.Requests[0].URL == "http://x/1"
	})

	// A live status change flows through.
	reg.SetBoxStatus(registry.BoxStatus{Name: "dev", State: "disconnected", LastError: "boom"})
	waitFor(t, 2*time.Second, func() bool {
		v, ok := c.last()
		return ok && len(v.Boxes) == 1 && v.Boxes[0].State == "disconnected"
	})
}

func TestModelMarksDaemonDownWhenServerStops(t *testing.T) {
	reg := registry.New(50)
	sock, stop := startServer(t, reg, nil)

	c := &collector{}
	m := NewModel(Options{Network: "unix", Addr: sock, Retry: 20 * time.Millisecond, OnUpdate: c.onUpdate})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	waitFor(t, 2*time.Second, func() bool {
		v, ok := c.last()
		return ok && v.Connected
	})

	stop() // daemon goes away

	waitFor(t, 2*time.Second, func() bool {
		v, ok := c.last()
		return ok && !v.Connected
	})
}

func TestModelReloadCallsThrough(t *testing.T) {
	reg := registry.New(50)
	called := make(chan struct{}, 1)
	sock, stop := startServer(t, reg, func() error { called <- struct{}{}; return nil })
	defer stop()

	m := NewModel(Options{Network: "unix", Addr: sock})
	if err := m.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("reload not invoked")
	}
}

func TestRequestsCappedToMax(t *testing.T) {
	reg := registry.New(500)
	sock, stop := startServer(t, reg, nil)
	defer stop()

	c := &collector{}
	m := NewModel(Options{Network: "unix", Addr: sock, Retry: 20 * time.Millisecond, MaxRequests: 3, OnUpdate: c.onUpdate})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	waitFor(t, 2*time.Second, func() bool { v, ok := c.last(); return ok && v.Connected })
	for i := range 6 {
		reg.RecordRequest(registry.RequestRecord{Box: "dev", URL: "http://x", Outcome: "launched"})
		_ = i
	}
	waitFor(t, 2*time.Second, func() bool {
		v, ok := c.last()
		return ok && len(v.Requests) == 3
	})
}
