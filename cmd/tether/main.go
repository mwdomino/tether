package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags="-X main.Version=...".
var Version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "tether",
		Short:         "Open URLs requested on a headless server in a browser on your desktop.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newHostCmd(), newOpenCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tether:", err)
		os.Exit(1)
	}
}
