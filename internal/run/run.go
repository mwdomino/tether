// Package run implements `tether run`: it wraps a command with an ephemeral
// browser shim so OAuth/SSO logins work on a headless box without installing
// anything. The shim is written to a temp dir, exposed via $BROWSER and $PATH
// for the child process only, and removed when the command exits.
package run

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mwdomino/tether/internal/shim"
)

// Config holds the host-connection settings forwarded to the backgrounded
// `tether open` invocations via the child environment.
type Config struct {
	Server    string
	Socket    string
	AuthToken string
	Timeout   time.Duration
}

// Setup creates an ephemeral shim directory and returns the environment the
// wrapped command should run with, plus a cleanup func that removes the dir.
// binaryPath is the tether binary the shim invokes. baseEnv is the parent
// environment to build on (typically os.Environ()).
func Setup(binaryPath string, baseEnv []string, cfg Config) (env []string, cleanup func() error, err error) {
	tmpDir, err := os.MkdirTemp("", "tether-run-")
	if err != nil {
		return nil, nil, fmt.Errorf("create shim dir: %w", err)
	}
	cleanup = func() error { return os.RemoveAll(tmpDir) }

	res, err := shim.Install(binaryPath, shim.Options{
		BinDir:         tmpDir,
		InstallXDGOpen: runtime.GOOS == "linux",
		ForceXDGOpen:   true,
	})
	if err != nil {
		_ = cleanup()
		return nil, nil, err
	}

	env = buildEnv(baseEnv, tmpDir, res.ShimPath, cfg)
	return env, cleanup, nil
}

// Exec runs name with args using the given environment and stdio, returning the
// command's exit code. A non-nil error means the command could not be started.
func Exec(env []string, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func buildEnv(baseEnv []string, shimDir, shimPath string, cfg Config) []string {
	env := append([]string(nil), baseEnv...)
	existingPath := lookup(env, "PATH")
	newPath := shimDir
	if existingPath != "" {
		newPath = shimDir + string(os.PathListSeparator) + existingPath
	}
	env = set(env, "PATH", newPath)
	env = set(env, "BROWSER", shimPath)
	if cfg.Server != "" {
		env = set(env, "TETHER_SERVER", cfg.Server)
	}
	if cfg.Socket != "" {
		env = set(env, "TETHER_SOCKET", cfg.Socket)
	}
	if cfg.AuthToken != "" {
		env = set(env, "TETHER_AUTH_TOKEN", cfg.AuthToken)
	}
	if cfg.Timeout > 0 {
		env = set(env, "TETHER_TIMEOUT", cfg.Timeout.String())
	}
	return env
}

func lookup(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

func set(env []string, key, value string) []string {
	prefix := key + "="
	entry := key + "=" + value
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}
