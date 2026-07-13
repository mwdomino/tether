package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/config"
	"github.com/mwdomino/tether/internal/daemon"
)

func newHostCmd() *cobra.Command {
	var (
		configPath    string
		socketDir     string
		controlSocket string
		browser       []string
	)
	c := &cobra.Command{
		Use:   "host",
		Short: "Run the host daemon: keep SSH forwards alive, open browsers, and serve status.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			cs := controlSocket
			if cs == "" {
				cfg, err := config.Load(path)
				if err != nil {
					return err
				}
				if cs, err = cfg.ControlSocketPath(); err != nil {
					return err
				}
			}
			d, err := daemon.New(daemon.Options{
				ConfigPath:    path,
				SocketDir:     socketDir,
				ControlSocket: cs,
				Browser:       browser,
			})
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			fmt.Fprintf(os.Stderr, "tether host: daemon starting (config %s)\n", path)
			return d.Run(ctx)
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "config file path (default ~/.config/tether/config.json)")
	c.Flags().StringVar(&socketDir, "socket-dir", "", "directory for per-box forward sockets (default $XDG_RUNTIME_DIR/tether)")
	c.Flags().StringVar(&controlSocket, "control-socket", "", "control/IPC socket path (default from config)")
	c.Flags().StringSliceVar(&browser, "browser", nil, "browser launch argv (URL appended)")
	return c
}
