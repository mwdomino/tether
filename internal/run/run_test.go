package run

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}

func TestSetupBuildsChildEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell shim test")
	}
	t.Setenv("HOME", t.TempDir())
	base := []string{"PATH=/usr/bin:/bin", "FOO=bar"}

	env, cleanup, err := Setup("/opt/tether/bin/tether", base, Config{
		Server:    "10.0.0.1:1234",
		AuthToken: "secret",
		Timeout:   90 * time.Second,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer cleanup()

	m := envMap(env)

	browser := m["BROWSER"]
	if filepath.Base(browser) != "tether-open" {
		t.Fatalf("BROWSER = %q, want a tether-open shim", browser)
	}
	info, err := os.Stat(browser)
	if err != nil {
		t.Fatalf("stat shim: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("shim %q is not executable: %v", browser, info.Mode())
	}

	parts := filepath.SplitList(m["PATH"])
	if len(parts) == 0 || parts[0] != filepath.Dir(browser) {
		t.Fatalf("PATH first entry = %v, want shim dir %q", parts, filepath.Dir(browser))
	}
	var sawUsrBin bool
	for _, p := range parts {
		if p == "/usr/bin" {
			sawUsrBin = true
		}
	}
	if !sawUsrBin {
		t.Fatalf("PATH dropped existing entries: %q", m["PATH"])
	}

	if m["FOO"] != "bar" {
		t.Fatalf("existing env not preserved: FOO=%q", m["FOO"])
	}
	if m["TETHER_SERVER"] != "10.0.0.1:1234" {
		t.Fatalf("TETHER_SERVER = %q", m["TETHER_SERVER"])
	}
	if m["TETHER_AUTH_TOKEN"] != "secret" {
		t.Fatalf("TETHER_AUTH_TOKEN = %q", m["TETHER_AUTH_TOKEN"])
	}
	if m["TETHER_TIMEOUT"] != "1m30s" {
		t.Fatalf("TETHER_TIMEOUT = %q", m["TETHER_TIMEOUT"])
	}
	if _, ok := m["TETHER_SOCKET"]; ok {
		t.Fatalf("TETHER_SOCKET should be unset when Socket is empty")
	}
}

func TestSetupSocketPassthrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell shim test")
	}
	t.Setenv("HOME", t.TempDir())

	env, cleanup, err := Setup("/opt/tether/bin/tether", os.Environ(), Config{
		Socket: "/run/tether.sock",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer cleanup()

	if got := envMap(env)["TETHER_SOCKET"]; got != "/run/tether.sock" {
		t.Fatalf("TETHER_SOCKET = %q", got)
	}
}

func TestSetupCleanupRemovesDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shell shim test")
	}
	t.Setenv("HOME", t.TempDir())

	env, cleanup, err := Setup("/opt/tether/bin/tether", os.Environ(), Config{})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	dir := filepath.Dir(envMap(env)["BROWSER"])
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("shim dir should exist before cleanup: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("shim dir should be gone after cleanup, stat err = %v", err)
	}
}

func TestExecPropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	code, err := Exec(os.Environ(), "sh", []string{"-c", "exit 7"}, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestExecZeroOnSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	code, err := Exec(os.Environ(), "sh", []string{"-c", "exit 0"}, nil, io.Discard, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("Exec = (%d, %v), want (0, nil)", code, err)
	}
}

func TestExecErrorsWhenCommandMissing(t *testing.T) {
	_, err := Exec(os.Environ(), "tether-no-such-binary-xyzzy", nil, nil, io.Discard, io.Discard)
	if err == nil {
		t.Fatalf("expected error for missing command")
	}
}
