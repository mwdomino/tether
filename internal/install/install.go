// Package install handles writing and removing the host daemon's
// platform-specific service file (systemd user unit on Linux, launchd
// LaunchAgent on macOS, Startup folder .cmd on Windows). All paths are
// user-scoped — no admin/root required.
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

// Options controls optional flags baked into the installed unit's ExecStart.
// Leave fields empty to use the host's built-in defaults.
type Options struct {
	// Listen is a TCP host:port (e.g., "127.0.0.1:9999"). Mutually exclusive
	// with Socket.
	Listen string
	// Socket is a unix socket path. Mutually exclusive with Listen.
	Socket string
	// AuthToken, if set, is passed as --auth-token to the host.
	AuthToken string
}

func (o Options) extraArgs() []string {
	var a []string
	if o.Listen != "" {
		a = append(a, "--listen", o.Listen)
	}
	if o.Socket != "" {
		a = append(a, "--socket", o.Socket)
	}
	if o.AuthToken != "" {
		a = append(a, "--auth-token", o.AuthToken)
	}
	return a
}

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
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return "", errors.New("APPDATA is unset")
		}
		return filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "tether-host.cmd"), nil
	default:
		return "", fmt.Errorf("install: unsupported OS %q", runtime.GOOS)
	}
}

// Install writes the service file for the host daemon and starts it.
// binaryPath is the absolute path to the tether binary the unit will invoke.
// opts.Listen / opts.Socket / opts.AuthToken, when set, are appended to the
// unit's ExecStart line.
func Install(binaryPath string, opts Options) error {
	if opts.Listen != "" && opts.Socket != "" {
		return errors.New("install: --listen and --socket are mutually exclusive")
	}
	path, err := UnitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	body := renderUnitFor(runtime.GOOS, binaryPath, opts.extraArgs())
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return enable(path, binaryPath, opts.extraArgs())
}

// Uninstall stops and removes the service file. Missing file is not an error.
func Uninstall() error {
	path, err := UnitPath()
	if err != nil {
		return err
	}
	_ = disable(path) // best-effort stop
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
	case "windows":
		args := append([]string{binaryPath, "host"}, extraArgs...)
		return fmt.Sprintf(`@echo off
start "" %s
`, joinWindowsArgs(args))
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

func joinWindowsArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, quoteWindowsArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteWindowsArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}

func enable(unitPath, binaryPath string, extraArgs []string) error {
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
	case "windows":
		// Launch the host immediately so the user does not need to log out and back in.
		args := append([]string{"/c", "start", "", binaryPath, "host"}, extraArgs...)
		return runDetached("cmd", args...)
	default:
		return fmt.Errorf("install: unsupported OS %q", runtime.GOOS)
	}
}

func disable(unitPath string) error {
	switch runtime.GOOS {
	case "linux":
		_ = run("systemctl", "--user", "disable", "--now", "tether-host.service")
		return nil
	case "darwin":
		uid := strconv.Itoa(os.Getuid())
		_ = run("launchctl", "bootout", "gui/"+uid+"/com.tether.host")
		return nil
	case "windows":
		_ = run("taskkill", "/F", "/IM", "tether.exe")
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

func runDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}
