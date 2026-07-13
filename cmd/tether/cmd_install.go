package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/install"
)

func newInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install",
		Short: "Install the host daemon as a per-user service that starts on login.",
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := currentExecutable()
			if err != nil {
				return err
			}
			if err := install.Install(abs); err != nil {
				return err
			}
			path, _ := install.UnitPath()
			fmt.Fprintf(os.Stderr, "tether: installed and started host daemon (%s)\n", path)
			fmt.Fprintln(os.Stderr, "tether: add a box with 'tether box add <name> --ssh-host <ssh-alias>', then 'tether reload'.")
			fmt.Fprintln(os.Stderr, "tether: check connections with 'tether status'.")
			return nil
		},
	}
	return c
}
