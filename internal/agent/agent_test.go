package agent

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mwdomino/tether/internal/host"
)

func startHost(t *testing.T, browserArgv []string) (network, addr string) {
	t.Helper()
	cfg := host.Config{Browser: browserArgv}
	cfg.Network, cfg.Addr = "tcp", "127.0.0.1:0"
	h, err := host.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- h.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for h.Addr() == nil {
		select {
		case err := <-errCh:
			skipIfSocketDenied(t, err)
			t.Fatalf("host failed to start: %v", err)
		default:
		}
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
	err := Run(ctx, Config{Network: "tcp", Addr: "127.0.0.1:1", URL: "https://example.com/"})
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

func TestAgentLoopbackEndToEnd(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "url.txt")

	// SSO tool target on the headless side.
	ssoLn, err := net.Listen("tcp", "127.0.0.1:0")
	skipIfSocketDenied(t, err)
	if err != nil {
		t.Fatal(err)
	}
	defer ssoLn.Close()
	go func() {
		conn, err := ssoLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nOK"))
	}()
	ssoPort := ssoLn.Addr().(*net.TCPAddr).Port

	// Pick a free desktop-side port the host should bind.
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	skipIfSocketDenied(t, err)
	if err != nil {
		t.Fatal(err)
	}
	desktopPort := tmp.Addr().(*net.TCPAddr).Port
	tmp.Close()

	// Shorten the host's tunnel grace period so the test isn't bound to the
	// 60-second production default.
	prevGrace := host.TunnelGracePeriod
	host.TunnelGracePeriod = 500 * time.Millisecond
	defer func() { host.TunnelGracePeriod = prevGrace }()

	network, addr := startHost(t, mockBrowserCmd(mark))

	// Run agent in a goroutine; it will block on tunnel relay until callback completes.
	url := "https://idp/auth?redirect_uri=http://localhost:" + itoaT(desktopPort) + "/cb"
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errCh <- Run(ctx, Config{
			Network: network,
			Addr:    addr,
			URL:     url,
			Timeout: 5 * time.Second,
			// Override: dial the SSO tool on its actual port, not desktopPort.
			LoopbackDialer: func(_ int) (net.Conn, error) {
				return net.Dial("tcp", "127.0.0.1:"+itoaT(ssoPort))
			},
		})
	}()

	// Wait for the desktop-side listener to be bound.
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.Dial("tcp", "127.0.0.1:"+itoaT(desktopPort))
		if err == nil {
			// Send synthetic HTTP callback.
			_, _ = c.Write([]byte("GET /cb?code=xyz HTTP/1.0\r\nHost: localhost\r\n\r\n"))
			buf := make([]byte, 256)
			n, _ := c.Read(buf)
			c.Close()
			if n == 0 || !strings.Contains(string(buf[:n]), "200 OK") {
				t.Fatalf("bad response: %q", string(buf[:n]))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desktop port never bound: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Agent should exit cleanly after the (shortened) grace period elapses.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("agent: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("agent did not exit in time")
	}
	_ = mark
}

func itoaT(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}

func skipIfSocketDenied(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("socket creation is not permitted in this environment: %v", err)
	}
}
