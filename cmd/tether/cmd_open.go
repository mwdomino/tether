package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/agent"
)

func newOpenCmd() *cobra.Command {
	var (
		server    string
		socket    string
		authToken string
		timeout   time.Duration
	)
	c := &cobra.Command{
		Use:   "open <url>",
		Short: "Send a URL to the desktop host to be opened in a browser.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			network, addr := resolveTarget(server, socket)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := agent.Config{
				Network:   network,
				Addr:      addr,
				URL:       args[0],
				AuthToken: authToken,
				Timeout:   timeout,
			}
			err := agent.Run(ctx, cfg)
			if err == nil {
				return nil
			}
			switch {
			case errors.Is(err, agent.ErrPortCollision):
				fmt.Fprintln(os.Stderr, "tether:", err)
				os.Exit(2)
			case errors.Is(err, agent.ErrHostUnreachable):
				fmt.Fprintf(os.Stderr, "tether: failed to connect to host on %s %s — is RemoteForward set up?\n", network, addr)
				os.Exit(3)
			case errors.Is(err, agent.ErrAuthMismatch):
				fmt.Fprintln(os.Stderr, "tether: auth token mismatch")
				os.Exit(4)
			case errors.Is(err, agent.ErrTimeout):
				fmt.Fprintln(os.Stderr, "tether: timed out waiting for callback")
				os.Exit(5)
			case errors.Is(err, agent.ErrBrowserLaunch):
				fmt.Fprintln(os.Stderr, "tether:", err)
				os.Exit(6)
			default:
				return err
			}
			return nil
		},
	}
	c.Flags().StringVar(&server, "server", envOr("TETHER_SERVER", "127.0.0.1:9999"), "host TCP address")
	c.Flags().StringVar(&socket, "socket", os.Getenv("TETHER_SOCKET"), "host unix socket path (overrides --server)")
	c.Flags().StringVar(&authToken, "auth-token", os.Getenv("TETHER_AUTH_TOKEN"), "shared secret (if host requires)")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "overall timeout including loopback wait")
	return c
}

func resolveTarget(server, socket string) (network, addr string) {
	if socket != "" {
		return "unix", socket
	}
	return "tcp", server
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
