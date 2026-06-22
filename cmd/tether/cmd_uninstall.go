package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/install"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the host daemon service.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := install.Uninstall(); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "tether: uninstalled")
			return nil
		},
	}
}
