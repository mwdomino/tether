package autostart

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistPathUsesHomeLaunchAgents(t *testing.T) {
	t.Setenv("HOME", "/Users/tester")
	a := Agent{Label: "io.example.app", Program: []string{"/bin/true"}}
	got, err := a.plistPath()
	if err != nil {
		t.Fatalf("plistPath: %v", err)
	}
	want := "/Users/tester/Library/LaunchAgents/io.example.app.plist"
	if got != want {
		t.Fatalf("plistPath = %q, want %q", got, want)
	}
}

func TestRenderContainsLabelProgramAndRunAtLoad(t *testing.T) {
	a := Agent{Label: "io.example.app", Program: []string{"/Applications/Tether.app/Contents/MacOS/Tether", "--flag"}}
	got := a.render()
	for _, want := range []string{
		"<string>io.example.app</string>",
		"<string>/Applications/Tether.app/Contents/MacOS/Tether</string>",
		"<string>--flag</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderEscapesXML(t *testing.T) {
	a := Agent{Label: "io.example.app", Program: []string{"/Apps/Tether & Co/Tether"}}
	got := a.render()
	if !strings.Contains(got, "/Apps/Tether &amp; Co/Tether") {
		t.Fatalf("program path not XML-escaped in:\n%s", got)
	}
	if strings.Contains(got, "Tether & Co") {
		t.Fatalf("raw ampersand leaked into plist:\n%s", got)
	}
}

func TestEnabledFalseWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	a := Agent{Label: "io.example.missing", Program: []string{"/bin/true"}}
	// Ensure the LaunchAgents dir exists but the plist does not.
	if err := ensureDir(filepath.Join(dir, "Library", "LaunchAgents")); err != nil {
		t.Fatal(err)
	}
	ok, err := a.Enabled()
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	if ok {
		t.Fatal("expected Enabled=false when plist absent")
	}
}
