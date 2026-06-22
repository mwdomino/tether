package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/install"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
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
			if err := install.Install(abs); err != nil {
				return err
			}
			path, _ := install.UnitPath()
			fmt.Fprintf(os.Stderr, "tether: installed and started (%s)\n", path)
			return nil
		},
	}
}
