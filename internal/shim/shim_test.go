package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesIdempotentShim(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	logDir := filepath.Join(dir, "logs")

	first, err := Install("/opt/tether/bin/tether", Options{BinDir: binDir, LogDir: logDir, InstallXDGOpen: true})
	if err != nil {
		t.Fatalf("Install first: %v", err)
	}
	second, err := Install("/opt/tether/bin/tether", Options{BinDir: binDir, LogDir: logDir, InstallXDGOpen: true})
	if err != nil {
		t.Fatalf("Install second: %v", err)
	}
	if first != second {
		t.Fatalf("result changed across installs: first=%+v second=%+v", first, second)
	}

	body, err := os.ReadFile(first.ShimPath)
	if err != nil {
		t.Fatalf("read shim: %v", err)
	}
	if !strings.Contains(string(body), "nohup '/opt/tether/bin/tether' open") {
		t.Fatalf("shim does not run tether open: %s", body)
	}
	target, err := os.Readlink(first.XDGOpenPath)
	if err != nil {
		t.Fatalf("readlink xdg-open: %v", err)
	}
	if target != first.ShimPath {
		t.Fatalf("xdg-open target = %q, want %q", target, first.ShimPath)
	}
}

func TestInstallDoesNotReplaceConflictingXDGOpen(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "xdg-open"), []byte("custom"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Install("/opt/tether/bin/tether", Options{
		BinDir:         binDir,
		LogDir:         filepath.Join(dir, "logs"),
		InstallXDGOpen: true,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected xdg-open conflict, got %v", err)
	}
}

func TestSourceScript(t *testing.T) {
	dir := t.TempDir()
	got, err := SourceScript(dir)
	if err != nil {
		t.Fatalf("SourceScript: %v", err)
	}
	wantPath := "export PATH='" + dir + "':\"$PATH\""
	wantBrowser := "export BROWSER='" + filepath.Join(dir, "tether-open") + "'"
	if !strings.Contains(got, wantPath) || !strings.Contains(got, wantBrowser) {
		t.Fatalf("unexpected source script:\n%s", got)
	}
}
