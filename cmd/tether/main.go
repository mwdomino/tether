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
		Short:         "Open URLs requested on a headless server in a browser on another machine.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newHostCmd(), newOpenCmd(), newRunCmd(), newInstallCmd(), newInstallShimCmd(), newSourceCmd(), newUninstallCmd(), newBoxCmd(), newStatusCmd(), newReloadCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tether:", err)
		os.Exit(1)
	}
}
