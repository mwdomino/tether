package agent

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mwdomino/tether/internal/host"
)

func startHost(t *testing.T, browserArgv []string) (network, addr string) {
	t.Helper()
	dir := t.TempDir()
	cfg := host.Config{Browser: browserArgv}
	if runtime.GOOS == "windows" {
		cfg.Network, cfg.Addr = "tcp", "127.0.0.1:0"
	} else {
		cfg.Network, cfg.Addr = "unix", filepath.Join(dir, "tether.sock")
	}
	h, err := host.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = h.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for h.Addr() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a := h.Addr()
	return a.Network(), a.String()
}

func TestAgentOpenNoLoopback(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "url.txt")
	browser := mockBrowserCmd(mark)

	network, addr := startHost(t, browser)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, Config{
		Network: network,
		Addr:    addr,
		URL:     "https://example.com/x",
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Wait for browser mark.
	deadline := time.Now().Add(3 * time.Second)
	var got []byte
	for {
		b, err := os.ReadFile(mark)
		if err == nil {
			got = b
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("browser mark not written: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(got) != "https://example.com/x" {
		t.Fatalf("browser got %q", string(got))
	}
}

func TestAgentHostUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Bind a port then immediately close to get a guaranteed unused one.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	a := ln.Addr().String()
	ln.Close()
	err := Run(ctx, Config{Network: "tcp", Addr: a, URL: "https://example.com/"})
	if err != ErrHostUnreachable {
		t.Fatalf("expected ErrHostUnreachable, got %v", err)
	}
}

func mockBrowserCmd(markPath string) []string {
	if runtime.GOOS == "windows" {
		return []string{"powershell", "-Command", "Set-Content -NoNewline -Path '" + markPath + "' -Value $args[0]", "--"}
	}
	return []string{"sh", "-c", "printf %s \"$1\" > '" + markPath + "'", "sh"}
}
