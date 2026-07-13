package control

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/mwdomino/tether/internal/registry"
)

func startServer(t *testing.T, reg *registry.Registry, onReload func() error) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket here: %v", err)
	}
	srv := NewServer(reg, onReload, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, ln) }()
	return sock
}

func TestSnapshotRoundTrip(t *testing.T) {
	reg := registry.New(10)
	reg.SetBoxStatus(registry.BoxStatus{Name: "dev", SSHHost: "dev-box", State: "connected"})
	reg.RecordRequest(registry.RequestRecord{Box: "dev", URL: "http://x/1", Outcome: "launched"})

	sock := startServer(t, reg, nil)

	snap, err := Snapshot("unix", sock)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Boxes) != 1 || snap.Boxes[0].Name != "dev" || snap.Boxes[0].State != "connected" {
		t.Fatalf("unexpected boxes: %+v", snap.Boxes)
	}
	if len(snap.Requests) != 1 || snap.Requests[0].URL != "http://x/1" {
		t.Fatalf("unexpected requests: %+v", snap.Requests)
	}
}

func TestSubscribeStreamsEvents(t *testing.T) {
	reg := registry.New(10)
	sock := startServer(t, reg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, events, err := Stream(ctx, "unix", sock)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Give the server a moment to register the subscription, then emit.
	time.Sleep(50 * time.Millisecond)
	reg.RecordRequest(registry.RequestRecord{Box: "dev", URL: "http://x/2", Outcome: "launched"})

	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("event channel closed early")
		}
		if ev.Kind != "request" || ev.Request == nil || ev.Request.URL != "http://x/2" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
}

func TestReloadInvokesCallback(t *testing.T) {
	reg := registry.New(10)
	called := make(chan struct{}, 1)
	sock := startServer(t, reg, func() error {
		called <- struct{}{}
		return nil
	})

	if err := Reload("unix", sock); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("reload callback not invoked")
	}
}
