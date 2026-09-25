package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStableExecutablePathUsesMatchingSymlink(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "Cellar", "tether", "1.0", "bin", "tether")
	if err := os.MkdirAll(filepath.Dir(versioned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "tether")
	if err := os.Symlink(versioned, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", linkDir)
	if got := stableExecutablePath(versioned); got != link {
		t.Fatalf("stableExecutablePath = %q, want %q", got, link)
	}
}

func TestStableExecutablePathIgnoresOtherInstallation(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tether-current")
	if err := os.WriteFile(exe, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "tether")
	if err := os.WriteFile(other, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := stableExecutablePath(exe); got != exe {
		t.Fatalf("stableExecutablePath = %q, want %q", got, exe)
	}
}

func TestStableExecutablePathWithoutPATHEntry(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tether-binary")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := stableExecutablePath(exe); got != exe {
		t.Fatalf("stableExecutablePath = %q, want %q", got, exe)
	}
}
