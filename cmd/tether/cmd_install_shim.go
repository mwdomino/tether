package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/shim"
)

func newInstallShimCmd() *cobra.Command {
	var opts shim.Options
	opts.InstallXDGOpen = runtime.GOOS == "linux"
	c := &cobra.Command{
		Use:   "install-shim",
		Short: "Install the agent-side browser shim used as BROWSER.",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := currentExecutable()
			if err != nil {
				return err
			}
			res, err := shim.Install(exe, opts)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "tether: installed headless shim (%s)\n", res.ShimPath)
			if res.XDGOpenPath != "" {
				fmt.Fprintf(os.Stderr, "tether: installed xdg-open shim (%s)\n", res.XDGOpenPath)
			}
			script, err := shim.SourceScript(opts.BinDir)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "tether: run this now, or add it to your shell profile:")
			fmt.Fprint(os.Stderr, script)
			return nil
		},
	}
	c.Flags().StringVar(&opts.BinDir, "bin-dir", "", "directory for the tether-open shim (default ~/.local/bin)")
	c.Flags().StringVar(&opts.LogDir, "log-dir", "", "directory for shim logs (default ~/.cache/tether)")
	c.Flags().BoolVar(&opts.InstallXDGOpen, "xdg-open", opts.InstallXDGOpen, "also install an xdg-open shim in --bin-dir")
	c.Flags().BoolVar(&opts.ForceXDGOpen, "force-xdg-open", false, "replace an existing --bin-dir/xdg-open")
	return c
}

func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return stableExecutablePath(abs), nil
}

// Prefer a PATH entry that points to this exact binary. Package managers can
// replace that entry on upgrade, while a resolved versioned path goes stale.
func stableExecutablePath(exe string) string {
	candidate, err := exec.LookPath("tether")
	if err != nil {
		return exe
	}
	running, err := os.Stat(exe)
	if err != nil {
		return exe
	}
	target, err := os.Stat(candidate)
	if err != nil || !os.SameFile(running, target) {
		return exe
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return exe
	}
	return abs
}
