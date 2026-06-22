package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/install"
)

func newInstallCmd() *cobra.Command {
	var opts install.Options
	c := &cobra.Command{
		Use:   "install",
		Short: "Install the host daemon as a per-user service that starts on login.",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate current binary: %w", err)
			}
			resolved, err := filepath.EvalSymlinks(exe)
			if err != nil {
				resolved = exe
			}
			abs, err := filepath.Abs(resolved)
			if err != nil {
				return err
			}
			if err := install.Install(abs, opts); err != nil {
				return err
			}
			path, _ := install.UnitPath()
			fmt.Fprintf(os.Stderr, "tether: installed and started (%s)\n", path)
			return nil
		},
	}
	c.Flags().StringVar(&opts.Listen, "listen", "", "TCP host:port for the host daemon to listen on (e.g., 127.0.0.1:7777). Defaults to 127.0.0.1:9999 on Windows; unused on Linux/macOS unless --socket is also empty.")
	c.Flags().StringVar(&opts.Socket, "socket", "", "Unix socket path for the host daemon to listen on. Mutually exclusive with --listen.")
	c.Flags().StringVar(&opts.AuthToken, "auth-token", "", "Optional shared secret the agent must present.")
	return c
}
