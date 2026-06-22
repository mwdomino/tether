package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/host"
)

func newHostCmd() *cobra.Command {
	var (
		socket    string
		listen    string
		browser   []string
		authToken string
	)
	c := &cobra.Command{
		Use:   "host",
		Short: "Run the desktop-side daemon that opens browsers and relays loopback callbacks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := host.Config{Browser: browser, AuthToken: authToken}
			switch {
			case listen != "":
				cfg.Network = "tcp"
				cfg.Addr = listen
			case socket != "":
				cfg.Network = "unix"
				cfg.Addr = socket
			default:
				if runtime.GOOS == "windows" {
					cfg.Network = "tcp"
					cfg.Addr = "127.0.0.1:9999"
				} else {
					cfg.Network = "unix"
					cfg.Addr = defaultSocketPath()
				}
			}
			h, err := host.New(cfg)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			fmt.Fprintf(os.Stderr, "tether host listening on %s %s\n", cfg.Network, cfg.Addr)
			return h.Serve(ctx)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "unix socket path to listen on (Linux/macOS)")
	c.Flags().StringVar(&listen, "listen", "", "TCP host:port to listen on (Windows default 127.0.0.1:9999)")
	c.Flags().StringSliceVar(&browser, "browser", nil, "browser launch argv (URL appended)")
	c.Flags().StringVar(&authToken, "auth-token", "", "optional shared secret required from agents")
	return c
}

func defaultSocketPath() string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "tether.sock")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/tether.sock"
	}
	dir := filepath.Join(home, ".local", "share", "tether")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "tether.sock")
}
