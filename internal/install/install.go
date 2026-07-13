// Package install handles writing and removing the host daemon's
// platform-specific service file (systemd user unit on Linux, launchd
// LaunchAgent on macOS). All paths are user-scoped — no admin/root required.
package install

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// unitPathOverride lets tests redirect the unit path. Empty in production.
var unitPathOverride string

// UnitPath returns the absolute path where the host daemon's service file
// is written on this OS.
func UnitPath() (string, error) {
	if unitPathOverride != "" {
		return unitPathOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	switch runtime.GOOS {
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "tether-host.service"), nil
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", "com.tether.host.plist"), nil
	default:
		return "", fmt.Errorf("install: unsupported OS %q", runtime.GOOS)
	}
}

// Install writes the service file for the host daemon and starts it.
// binaryPath is the absolute path to the tether binary the unit will invoke
// as `tether host` (which reads its box list from the config file).
func Install(binaryPath string) error {
	path, err := UnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	body := renderUnitFor(runtime.GOOS, binaryPath, nil)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return enable(path)
}

// Uninstall stops and removes the service file. Missing file is not an error.
func Uninstall() error {
	path, err := UnitPath()
	if err != nil {
		return err
	}
	_ = disable() // best-effort stop
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return postUninstall()
}

// renderUnit is a convenience wrapper for the current OS; tests use it for
// quick rendering without going through Install.
func renderUnit(binaryPath string) string {
	return renderUnitFor(runtime.GOOS, binaryPath, nil)
}

func renderUnitFor(goos, binaryPath string, extraArgs []string) string {
	switch goos {
	case "linux":
		args := append([]string{binaryPath, "host"}, extraArgs...)
		execStart := joinSystemdArgs(args)
		return fmt.Sprintf(`[Unit]
Description=Tether host daemon (remote browser opener)
After=default.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, execStart)
	case "darwin":
		argStrings := []string{
			fmt.Sprintf("        <string>%s</string>", xmlEscape(binaryPath)),
			"        <string>host</string>",
		}
		for _, a := range extraArgs {
			argStrings = append(argStrings, fmt.Sprintf("        <string>%s</string>", xmlEscape(a)))
		}
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.tether.host</string>
    <key>ProgramArguments</key>
    <array>
%s
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`, strings.Join(argStrings, "\n"))
	default:
		return ""
	}
}

func joinSystemdArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteSystemdArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteSystemdArg(arg string) string {
	if arg == "" {
		return `""`
	}
	for _, r := range arg {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("/._:=@%+-", r)) {
			escaped := strings.ReplaceAll(arg, `\\`, `\\\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			return `"` + escaped + `"`
		}
	}
	return arg
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func enable(unitPath string) error {
	switch runtime.GOOS {
	case "linux":
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return run("systemctl", "--user", "enable", "--now", "tether-host.service")
	case "darwin":
		uid := strconv.Itoa(os.Getuid())
		// Prefer modern bootstrap; fall back to legacy load -w.
		if err := run("launchctl", "bootstrap", "gui/"+uid, unitPath); err != nil {
			return run("launchctl", "load", "-w", unitPath)
		}
		return nil
	default:
		return fmt.Errorf("install: unsupported OS %q", runtime.GOOS)
	}
}

func disable() error {
	switch runtime.GOOS {
	case "linux":
		_ = run("systemctl", "--user", "disable", "--now", "tether-host.service")
		return nil
	case "darwin":
		uid := strconv.Itoa(os.Getuid())
		_ = run("launchctl", "bootout", "gui/"+uid+"/com.tether.host")
		return nil
	default:
		return nil
	}
}

func postUninstall() error {
	if runtime.GOOS == "linux" {
		_ = run("systemctl", "--user", "daemon-reload")
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
