package install

import (
	"os"
	"path/filepath"
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
	case "windows":
		if !strings.HasSuffix(p, "tether-host.cmd") {
			t.Fatalf("unexpected windows path: %s", p)
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
