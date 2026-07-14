// Package autostart manages a per-user macOS launchd LaunchAgent so an app
// starts automatically at login. The plist rendering and path logic are
// platform-neutral (and unit-tested); Enable/Disable shell out to launchctl,
// which is a no-op path outside macOS.
package autostart

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Agent describes a login item: a launchd label and the program argv to run.
type Agent struct {
	Label   string
	Program []string
}

func (a Agent) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", a.Label+".plist"), nil
}

func (a Agent) render() string {
	var args strings.Builder
	for _, p := range a.Program {
		fmt.Fprintf(&args, "        <string>%s</string>\n", xmlEscape(p))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
%s    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`, xmlEscape(a.Label), args.String())
}

// Enabled reports whether the login item plist is installed.
func (a Agent) Enabled() (bool, error) {
	path, err := a.plistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Enable writes the plist and loads it so the app launches now and at login.
func (a Agent) Enable() error {
	if len(a.Program) == 0 {
		return errors.New("autostart: Program is required")
	}
	path, err := a.plistPath()
	if err != nil {
		return err
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(a.render()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	uid := strconv.Itoa(os.Getuid())
	// Reload cleanly: bootout any prior instance, then bootstrap.
	_ = run("launchctl", "bootout", "gui/"+uid+"/"+a.Label)
	if err := run("launchctl", "bootstrap", "gui/"+uid, path); err != nil {
		// Fall back to the legacy loader.
		return run("launchctl", "load", "-w", path)
	}
	return nil
}

// Disable unloads and removes the login item. A missing plist is not an error.
func (a Agent) Disable() error {
	path, err := a.plistPath()
	if err != nil {
		return err
	}
	uid := strconv.Itoa(os.Getuid())
	_ = run("launchctl", "bootout", "gui/"+uid+"/"+a.Label)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
