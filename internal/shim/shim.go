// Package shim installs the headless-side browser shim used as $BROWSER.
package shim

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Options controls headless-side shim installation.
type Options struct {
	// BinDir is where tether-open is written. Defaults to ~/.local/bin on
	// Unix-like systems and %LOCALAPPDATA%\tether\bin on Windows.
	BinDir string
	// LogDir is where tether-open writes open.log. Defaults to ~/.cache/tether
	// on Unix-like systems and %LOCALAPPDATA%\tether on Windows.
	LogDir string
	// InstallXDGOpen creates or updates BinDir/xdg-open as a symlink to the shim.
	InstallXDGOpen bool
	// ForceXDGOpen replaces an existing non-shim BinDir/xdg-open.
	ForceXDGOpen bool
}

// Result describes installed paths.
type Result struct {
	ShimPath    string
	XDGOpenPath string
	BinDir      string
	LogDir      string
}

// Install writes an idempotent headless-side browser shim.
func Install(binaryPath string, opts Options) (Result, error) {
	if binaryPath == "" {
		return Result{}, errors.New("shim: binary path required")
	}
	binDir, err := defaultedBinDir(opts.BinDir)
	if err != nil {
		return Result{}, err
	}
	logDir, err := defaultedLogDir(opts.LogDir)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir %s: %w", binDir, err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir %s: %w", logDir, err)
	}

	shimPath := filepath.Join(binDir, shimName())
	body := renderShim(binaryPath, logDir)
	if err := writeFileIfChanged(shimPath, []byte(body), 0o755); err != nil {
		return Result{}, err
	}

	res := Result{ShimPath: shimPath, BinDir: binDir, LogDir: logDir}
	if opts.InstallXDGOpen {
		if runtime.GOOS == "windows" {
			return Result{}, errors.New("shim: xdg-open shim is only supported on Unix-like systems")
		}
		xdgPath := filepath.Join(binDir, "xdg-open")
		if err := installSymlink(xdgPath, shimPath, opts.ForceXDGOpen); err != nil {
			return Result{}, err
		}
		res.XDGOpenPath = xdgPath
	}
	return res, nil
}

// SourceScript returns shell code that configures the current shell for tether.
func SourceScript(binDir string) (string, error) {
	binDir, err := defaultedBinDir(binDir)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("$env:Path = %q + ';' + $env:Path\n$env:BROWSER = %q\n",
			binDir, filepath.Join(binDir, shimName())), nil
	}
	return fmt.Sprintf("export PATH=%s:\"$PATH\"\nexport BROWSER=%s\n",
		shQuote(binDir), shQuote(filepath.Join(binDir, shimName()))), nil
}

func defaultedBinDir(binDir string) (string, error) {
	if binDir != "" {
		return filepath.Abs(binDir)
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA is unset")
		}
		return filepath.Join(base, "tether", "bin"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func defaultedLogDir(logDir string) (string, error) {
	if logDir != "" {
		return filepath.Abs(logDir)
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA is unset")
		}
		return filepath.Join(base, "tether"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".cache", "tether"), nil
}

func shimName() string {
	if runtime.GOOS == "windows" {
		return "tether-open.cmd"
	}
	return "tether-open"
}

func renderShim(binaryPath, logDir string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("@echo off\r\nstart \"\" /b %s open %%* ^>^> %s 2^>^&1\r\nexit /b 0\r\n",
			winQuote(binaryPath), winQuote(filepath.Join(logDir, "open.log")))
	}
	return fmt.Sprintf(`#!/usr/bin/env sh
mkdir -p %s
nohup %s open "$@" >>%s 2>&1 &
exit 0
`, shQuote(logDir), shQuote(binaryPath), shQuote(filepath.Join(logDir, "open.log")))
}

func writeFileIfChanged(path string, body []byte, mode os.FileMode) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(body) {
		return os.Chmod(path, mode)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func installSymlink(path, target string, force bool) error {
	if existingTarget, err := os.Readlink(path); err == nil {
		if existingTarget == target {
			return nil
		}
		if !force {
			return fmt.Errorf("%s already points to %s; rerun with --force-xdg-open to replace it", path, existingTarget)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// Nothing to remove.
	} else if force {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	} else {
		return fmt.Errorf("%s already exists and is not a symlink; rerun with --force-xdg-open to replace it", path)
	}
	if err := os.Symlink(target, path); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", path, target, err)
	}
	return nil
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func winQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
