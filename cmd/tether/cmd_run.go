package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/run"
)

const runUsage = "usage: tether run [flags] -- <command> [args...]"

func newRunCmd() *cobra.Command {
	var cfg run.Config
	c := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run a command with an ephemeral browser shim (no install required).",
		Long: "Run wraps a command with a temporary $BROWSER (and, on Linux, xdg-open)\n" +
			"shim, so OAuth/SSO logins on an agent reach your host without\n" +
			"installing anything. The shim is removed when the command exits.\n\n" +
			"Separate tether's flags from the wrapped command with --:\n" +
			"  tether run -- aws sso login\n" +
			"  tether run --timeout 10m -- gh auth login",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, childArgs, err := childCommand(args, cmd.ArgsLenAtDash())
			if err != nil {
				return err
			}
			exe, err := currentExecutable()
			if err != nil {
				return err
			}
			env, cleanup, err := run.Setup(exe, os.Environ(), cfg)
			if err != nil {
				return err
			}
			defer cleanup()

			code, err := run.Exec(env, name, childArgs, os.Stdin, os.Stdout, os.Stderr)
			if err != nil {
				return fmt.Errorf("run %s: %w", name, err)
			}
			_ = cleanup()
			os.Exit(code)
			return nil
		},
	}
	c.Flags().StringVar(&cfg.Server, "server", envOr("TETHER_SERVER", "127.0.0.1:9999"), "host TCP address")
	c.Flags().StringVar(&cfg.Socket, "socket", os.Getenv("TETHER_SOCKET"), "host unix socket path (overrides --server)")
	c.Flags().StringVar(&cfg.AuthToken, "auth-token", os.Getenv("TETHER_AUTH_TOKEN"), "shared secret (if host requires)")
	c.Flags().DurationVar(&cfg.Timeout, "timeout", envDurationOr("TETHER_TIMEOUT", 5*time.Minute), "overall timeout including loopback wait")
	return c
}

// childCommand extracts the wrapped command from the args cobra collected.
// dashPos is cmd.ArgsLenAtDash(): -1 when no -- was given, otherwise the number
// of positional args that preceded it.
func childCommand(args []string, dashPos int) (name string, rest []string, err error) {
	if dashPos < 0 {
		return "", nil, errors.New("missing '--'; " + runUsage)
	}
	cmdArgs := args[dashPos:]
	if len(cmdArgs) == 0 {
		return "", nil, errors.New("no command after '--'; " + runUsage)
	}
	return cmdArgs[0], cmdArgs[1:], nil
}
