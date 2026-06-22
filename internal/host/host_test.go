package host

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/mwdomino/tether/internal/proto"
)

// startHost returns a started host and its dial address.
func startHost(t *testing.T, cfg Config) (h *Host, dial func() net.Conn) {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = h.Serve(ctx) }()
	// Wait briefly for Addr to become available.
	deadline := time.Now().Add(2 * time.Second)
	for h.Addr() == nil {
		if time.Now().After(deadline) {
			t.Fatal("host did not start in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	addr := h.Addr()
	return h, func() net.Conn {
		c, err := net.Dial(addr.Network(), addr.String())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return c
	}
}

func TestHostOpenURLNoLoopback(t *testing.T) {
	// Mock browser command: writes its last arg into a temp file.
	dir := t.TempDir()
	mark := filepath.Join(dir, "url.txt")
	browser := mockBrowserCmd(t, mark)

	cfg := Config{
		Network: "tcp",
		Addr:    "127.0.0.1:0",
		Browser: browser,
	}
	if runtime.GOOS != "windows" {
		// Prefer unix socket on non-Windows so we exercise the default path.
		cfg.Network = "unix"
		cfg.Addr = filepath.Join(dir, "tether.sock")
	}
	_, dial := startHost(t, cfg)

	conn := dial()
	defer conn.Close()
	session, err := yamux.Client(conn, nil)
	if err != nil {
		t.Fatalf("yamux.Client: %v", err)
	}
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := proto.WriteFrame(stream, proto.Request{URL: "https://example.com/"}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var resp proto.Response
	if err := proto.ReadFrame(stream, &resp); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not ok: %+v", resp)
	}
	// Browser was invoked; check the mark file.
	deadline := time.Now().Add(3 * time.Second)
	var got []byte
	for {
		got, err = os.ReadFile(mark)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("browser mark not written: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if string(got) != "https://example.com/" {
		t.Fatalf("browser got %q, want https://example.com/", string(got))
	}
}

// mockBrowserCmd returns an argv whose final argument (the URL) is written
// to markPath when the command runs.
func mockBrowserCmd(t *testing.T, markPath string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// cmd /c (set /p =) > markPath — easier to use PowerShell.
		return []string{"powershell", "-Command", "Set-Content -NoNewline -Path '" + markPath + "' -Value $args[0]", "--"}
	}
	return []string{"sh", "-c", "printf %s \"$1\" > " + shQuote(markPath), "sh"}
}

func shQuote(s string) string {
	return "'" + s + "'"
}
