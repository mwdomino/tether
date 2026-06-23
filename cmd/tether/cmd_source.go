package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/shim"
)

func newSourceCmd() *cobra.Command {
	var binDir string
	c := &cobra.Command{
		Use:   "source",
		Short: "Print shell exports for the headless-side shim.",
		RunE: func(cmd *cobra.Command, args []string) error {
			script, err := shim.SourceScript(binDir)
			if err != nil {
				return err
			}
			fmt.Print(script)
			return nil
		},
	}
	c.Flags().StringVar(&binDir, "bin-dir", "", "directory containing the tether-open shim (default ~/.local/bin)")
	return c
}
