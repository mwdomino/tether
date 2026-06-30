package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/install"
)

func newInstallCmd() *cobra.Command {
	var opts install.Options
	c := &cobra.Command{
		Use:   "install",
		Short: "Install the host daemon as a per-user service that starts on login.",
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := currentExecutable()
			if err != nil {
				return err
			}
			if err := install.Install(abs, opts); err != nil {
				return err
			}
			path, _ := install.UnitPath()
			fmt.Fprintf(os.Stderr, "tether: installed and started (%s)\n", path)
			fmt.Fprintln(os.Stderr, "tether: add this to ~/.ssh/config on this host:")
			fmt.Fprintf(os.Stderr, "  RemoteForward 9999 %s\n", remoteForwardTarget(opts))
			return nil
		},
	}
	c.Flags().StringVar(&opts.Listen, "listen", "", "TCP host:port for the host daemon to listen on (e.g., 127.0.0.1:7777). Defaults to 127.0.0.1:9999 on Windows; unused on Linux/macOS unless --socket is also empty.")
	c.Flags().StringVar(&opts.Socket, "socket", "", "Unix socket path for the host daemon to listen on. Mutually exclusive with --listen.")
	c.Flags().StringVar(&opts.AuthToken, "auth-token", "", "Optional shared secret the agent must present.")
	return c
}

func remoteForwardTarget(opts install.Options) string {
	if opts.Listen != "" {
		return opts.Listen
	}
	if opts.Socket != "" {
		return opts.Socket
	}
	if runtime.GOOS == "windows" {
		return "127.0.0.1:9999"
	}
	return defaultSocketPath()
}
