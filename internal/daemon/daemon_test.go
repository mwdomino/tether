package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/mwdomino/tether/internal/config"
	"github.com/mwdomino/tether/internal/control"
	"github.com/mwdomino/tether/internal/proto"
	"github.com/mwdomino/tether/internal/supervisor"
)

// aliveRunner simulates an ssh process that stays connected until ctx ends.
func aliveRunner(ctx context.Context, name string, args ...string) supervisor.Cmd {
	return &aliveCmd{ctx: ctx}
}

type aliveCmd struct{ ctx context.Context }

func (c *aliveCmd) Start() error { return nil }
func (c *aliveCmd) Wait() (string, error) {
	<-c.ctx.Done()
	return "", c.ctx.Err()
}

func mockBrowser(markPath string) []string {
	return []string{"sh", "-c", "printf %s \"$1\" > '" + markPath + "'", "sh"}
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
	t.Fatalf("condition not met within %v", timeout)
}

func newTestDaemon(t *testing.T, cfgPath, markPath string) (*Daemon, string, string) {
	t.Helper()
	dir := t.TempDir()
	socketDir := filepath.Join(dir, "sockets")
	controlSock := filepath.Join(dir, "control.sock")
	d, err := New(Options{
		ConfigPath:    cfgPath,
		SocketDir:     socketDir,
		ControlSocket: controlSock,
		Browser:       mockBrowser(markPath),
		Runner:        aliveRunner,
		ConnectGrace:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, socketDir, controlSock
}

func TestDaemonServesBoxAndRecordsRequest(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := &config.Config{}
	_ = cfg.AddBox(config.Box{Name: "dev", SSHHost: "localhost"})
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "url.txt")

	d, socketDir, controlSock := newTestDaemon(t, cfgPath, mark)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	boxSock := filepath.Join(socketDir, "dev.sock")
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(boxSock)
		return err == nil
	})

	// Simulate the tunnel: connect to the per-box socket as the agent would.
	conn, err := net.Dial("unix", boxSock)
	if err != nil {
		t.Fatalf("dial box socket: %v", err)
	}
	defer conn.Close()
	session, err := yamux.Client(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.WriteFrame(stream, proto.Request{URL: "https://example.com/"}); err != nil {
		t.Fatal(err)
	}
	var resp proto.Response
	if err := proto.ReadFrame(stream, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("request rejected: %+v", resp)
	}

	// The request must show up in the control snapshot, attributed to "dev".
	waitFor(t, 2*time.Second, func() bool {
		snap, err := control.Snapshot("unix", controlSock)
		if err != nil {
			return false
		}
		for _, r := range snap.Requests {
			if r.Box == "dev" && r.URL == "https://example.com/" && r.Outcome == "launched" {
				return true
			}
		}
		return false
	})

	// And the box should be reported connected.
	waitFor(t, 2*time.Second, func() bool {
		snap, err := control.Snapshot("unix", controlSock)
		if err != nil {
			return false
		}
		return len(snap.Boxes) == 1 && snap.Boxes[0].Name == "dev" && snap.Boxes[0].State == string(supervisor.StateConnected)
	})
}

func TestDaemonReloadPicksUpNewBox(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := config.Save(cfgPath, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "url.txt")

	d, socketDir, controlSock := newTestDaemon(t, cfgPath, mark)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	// Initially no boxes.
	waitFor(t, 2*time.Second, func() bool {
		snap, err := control.Snapshot("unix", controlSock)
		return err == nil && len(snap.Boxes) == 0
	})

	// Add a box to the config file and reload via the control socket.
	cfg, _ := config.Load(cfgPath)
	_ = cfg.AddBox(config.Box{Name: "prod", SSHHost: "prod-box"})
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := control.Reload("unix", controlSock); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(socketDir, "prod.sock"))
		if err != nil {
			return false
		}
		snap, err := control.Snapshot("unix", controlSock)
		return err == nil && len(snap.Boxes) == 1 && snap.Boxes[0].Name == "prod"
	})
}
