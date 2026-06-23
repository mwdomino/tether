package host

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	errCh := make(chan error, 1)
	go func() { errCh <- h.Serve(ctx) }()
	// Wait briefly for Addr to become available.
	deadline := time.Now().Add(2 * time.Second)
	for h.Addr() == nil {
		select {
		case err := <-errCh:
			skipIfSocketDenied(t, err)
			t.Fatalf("host failed to start: %v", err)
		default:
		}
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

func TestHostLoopbackRoundTrip(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "url.txt")
	browser := mockBrowserCmd(t, mark)

	cfg := Config{Browser: browser}
	cfg.Network, cfg.Addr = "tcp", "127.0.0.1:0"
	_, dial := startHost(t, cfg)

	// "headless-side SSO tool": a listener that echoes a fixed HTTP response.
	ssoLn, err := net.Listen("tcp", "127.0.0.1:0")
	skipIfSocketDenied(t, err)
	if err != nil {
		t.Fatalf("ssoLn: %v", err)
	}
	defer ssoLn.Close()
	go func() {
		conn, err := ssoLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the request line and respond.
		br := bufio.NewReader(conn)
		_, _ = br.ReadString('\n')
		_, _ = conn.Write([]byte("HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nOK"))
	}()
	ssoPort := ssoLn.Addr().(*net.TCPAddr).Port

	// Pick an unused desktop port to ask the host to bind. Using port 0
	// in the protocol isn't possible — pick one with Listen+Close.
	tmp, err := net.Listen("tcp", "127.0.0.1:0")
	skipIfSocketDenied(t, err)
	if err != nil {
		t.Fatal(err)
	}
	desktopPort := tmp.Addr().(*net.TCPAddr).Port
	tmp.Close()

	conn := dial()
	defer conn.Close()
	session, err := yamux.Client(conn, nil)
	if err != nil {
		t.Fatal(err)
	}
	control, _ := session.OpenStream()
	if err := proto.WriteFrame(control, proto.Request{
		URL:           "https://idp/auth?redirect_uri=http://localhost:" + itoa(desktopPort) + "/cb",
		LoopbackPorts: []int{desktopPort},
	}); err != nil {
		t.Fatal(err)
	}
	var resp proto.Response
	if err := proto.ReadFrame(control, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("host rejected request: %+v", resp)
	}

	// Accept the substream the host will open in response to a desktop
	// connection. The "agent" role runs in this test.
	streamCh := make(chan *yamux.Stream, 1)
	go func() {
		s, _ := session.AcceptStream()
		streamCh <- s
	}()

	// Connect to the desktop-side bound port — emulates the browser hitting
	// the SSO callback URL.
	desktopConn, err := net.Dial("tcp", "127.0.0.1:"+itoa(desktopPort))
	if err != nil {
		t.Fatalf("dial desktop port: %v", err)
	}
	defer desktopConn.Close()

	s := <-streamCh
	defer s.Close()
	var hdr proto.TunnelHeader
	if err := proto.ReadFrame(s, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Kind != "tunnel" || hdr.Port != desktopPort {
		t.Fatalf("unexpected header: %+v", hdr)
	}

	// Now play the agent's relay role: pipe desktopConn ↔ s and ssoPort target.
	sso, err := net.Dial("tcp", "127.0.0.1:"+itoa(ssoPort))
	if err != nil {
		t.Fatal(err)
	}
	defer sso.Close()
	go io.Copy(sso, s)
	go io.Copy(s, sso)

	// Send a synthetic HTTP request through desktopConn.
	_, _ = desktopConn.Write([]byte("GET /cb?code=abc HTTP/1.0\r\nHost: localhost\r\n\r\n"))
	br := bufio.NewReader(desktopConn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.HasPrefix(line, "HTTP/1.0 200") {
		t.Fatalf("unexpected response: %q", line)
	}
}

func TestHostLoopbackPortCollision(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Browser: mockBrowserCmd(t, filepath.Join(dir, "url.txt"))}
	cfg.Network, cfg.Addr = "tcp", "127.0.0.1:0"
	_, dial := startHost(t, cfg)

	// Hold a port on the desktop side so the host's bind will fail.
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	skipIfSocketDenied(t, err)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	port := hold.Addr().(*net.TCPAddr).Port

	conn := dial()
	defer conn.Close()
	session, _ := yamux.Client(conn, nil)
	stream, _ := session.OpenStream()
	_ = proto.WriteFrame(stream, proto.Request{
		URL:           "https://idp/auth?redirect_uri=http://localhost:" + itoa(port) + "/cb",
		LoopbackPorts: []int{port},
	})
	var resp proto.Response
	_ = proto.ReadFrame(stream, &resp)
	if resp.OK {
		t.Fatalf("expected port collision rejection, got OK")
	}
	if !strings.Contains(resp.Error, "already in use") {
		t.Fatalf("unexpected error: %q", resp.Error)
	}
}

func itoa(n int) string {
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
