package install

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestUnitPathPerOS verifies the per-OS unit path location.
func TestUnitPathPerOS(t *testing.T) {
	p, err := UnitPath()
	if err != nil {
		t.Fatalf("UnitPath: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Fatalf("UnitPath returned non-absolute path: %s", p)
	}
	switch runtime.GOOS {
	case "linux":
		if !strings.HasSuffix(p, "/.config/systemd/user/tether-host.service") {
			t.Fatalf("unexpected linux path: %s", p)
		}
	case "darwin":
		if !strings.HasSuffix(p, "/Library/LaunchAgents/com.tether.host.plist") {
			t.Fatalf("unexpected darwin path: %s", p)
		}
	}
}

// TestRenderUnitContainsBinary verifies the rendered file mentions the binary path.
func TestRenderUnitContainsBinary(t *testing.T) {
	got := renderUnit("/opt/tether/tether")
	if !strings.Contains(got, "/opt/tether/tether") {
		t.Fatalf("rendered unit does not contain binary path: %s", got)
	}
}

func TestRenderLinuxUnitQuotesExecStartArguments(t *testing.T) {
	got := renderUnitFor("linux", "/opt/tether bin/tether", []string{"--auth-token", `tok with " quote`})
	want := `ExecStart="/opt/tether bin/tether" host --auth-token "tok with \" quote"`
	if !strings.Contains(got, want) {
		t.Fatalf("linux ExecStart not safely quoted; want substring %q in:\n%s", want, got)
	}
}

func TestRenderDarwinUnitEscapesXMLArguments(t *testing.T) {
	got := renderUnitFor("darwin", "/Applications/Tether & Tools/tether", []string{"--auth-token", "a<b&c"})
	for _, want := range []string{"/Applications/Tether &amp; Tools/tether", "a&lt;b&amp;c"} {
		if !strings.Contains(got, want) {
			t.Fatalf("darwin plist missing escaped substring %q in:\n%s", want, got)
		}
	}
}

func TestEnableLinuxRestartsOnReinstall(t *testing.T) {
	var calls [][]string
	invoke := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	if err := enableFor("linux", "1000", "/unused", invoke); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "tether-host.service"},
		{"systemctl", "--user", "restart", "tether-host.service"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestEnableDarwinReplacesLoadedAgent(t *testing.T) {
	var calls [][]string
	invoke := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if len(args) > 0 && args[0] == "bootout" {
			return errors.New("not loaded")
		}
		return nil
	}
	if err := enableFor("darwin", "501", "/tmp/tether.plist", invoke); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"launchctl", "bootout", "gui/501/com.tether.host"},
		{"launchctl", "bootstrap", "gui/501", "/tmp/tether.plist"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// TestUninstallMissingIsNoop verifies uninstall does not error when no file is present.
func TestUninstallMissingIsNoop(t *testing.T) {
	// Redirect UnitPath into a temp dir for the duration of this test.
	dir := t.TempDir()
	prev := unitPathOverride
	unitPathOverride = filepath.Join(dir, "missing-tether-host")
	t.Cleanup(func() { unitPathOverride = prev })

	if _, err := os.Stat(unitPathOverride); err == nil {
		t.Fatal("test setup error: file should not exist")
	}
	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall on missing file returned: %v", err)
	}
}
