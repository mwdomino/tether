package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/config"
	"github.com/mwdomino/tether/internal/control"
	"github.com/mwdomino/tether/internal/registry"
)

func newStatusCmd() *cobra.Command {
	var (
		configPath string
		socket     string
		watch      bool
	)
	c := &cobra.Command{
		Use:   "status",
		Short: "Show each box's connection status and recent open requests.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, err := resolveControlSocket(socket, configPath)
			if err != nil {
				return err
			}
			if !watch {
				snap, err := control.Snapshot("unix", sock)
				if err != nil {
					return daemonError(err)
				}
				printSnapshot(snap)
				return nil
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			snap, events, err := control.Stream(ctx, "unix", sock)
			if err != nil {
				return daemonError(err)
			}
			printSnapshot(snap)
			fmt.Fprintln(os.Stdout, "\nwatching for changes (ctrl-c to stop)…")
			for ev := range events {
				printEvent(ev)
			}
			return nil
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "config file path (default ~/.config/tether/config.json)")
	c.Flags().StringVar(&socket, "socket", "", "control socket path (overrides config)")
	c.Flags().BoolVarP(&watch, "watch", "w", false, "stream live updates until interrupted")
	return c
}

func newReloadCmd() *cobra.Command {
	var (
		configPath string
		socket     string
	)
	c := &cobra.Command{
		Use:   "reload",
		Short: "Tell the running host daemon to re-read its config and reconcile boxes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, err := resolveControlSocket(socket, configPath)
			if err != nil {
				return err
			}
			if err := control.Reload("unix", sock); err != nil {
				return daemonError(err)
			}
			fmt.Fprintln(os.Stderr, "tether: reloaded")
			return nil
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "config file path (default ~/.config/tether/config.json)")
	c.Flags().StringVar(&socket, "socket", "", "control socket path (overrides config)")
	return c
}

func resolveControlSocket(socketFlag, configFlag string) (string, error) {
	if socketFlag != "" {
		return socketFlag, nil
	}
	path, err := resolveConfigPath(configFlag)
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	return cfg.ControlSocketPath()
}

func daemonError(err error) error {
	return fmt.Errorf("cannot reach host daemon (%v) — is 'tether host' running?", err)
}

func stateGlyph(state string) string {
	switch state {
	case "connected":
		return "●"
	case "connecting":
		return "◐"
	default:
		return "○"
	}
}

func printSnapshot(snap registry.Snapshot) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BOX\tSSH HOST\tSTATUS\tDETAIL")
	if len(snap.Boxes) == 0 {
		fmt.Fprintln(w, "(no boxes configured)")
	}
	for _, b := range snap.Boxes {
		fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\n", stateGlyph(b.State), b.Name, b.SSHHost, b.State, b.LastError)
	}
	_ = w.Flush()

	fmt.Fprintln(os.Stdout, "\nRecent requests:")
	rw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(rw, "TIME\tBOX\tURL\tOUTCOME")
	if len(snap.Requests) == 0 {
		fmt.Fprintln(rw, "(none yet)")
	}
	for _, r := range snap.Requests {
		fmt.Fprintf(rw, "%s\t%s\t%s\t%s\n", r.Time.Format("15:04:05"), r.Box, truncate(r.URL, 60), r.Outcome)
	}
	_ = rw.Flush()
}

func printEvent(ev registry.Event) {
	switch {
	case ev.Status != nil:
		s := ev.Status
		detail := ""
		if s.LastError != "" {
			detail = " — " + s.LastError
		}
		fmt.Fprintf(os.Stdout, "%s %s: %s%s\n", stateGlyph(s.State), s.Name, s.State, detail)
	case ev.Request != nil:
		r := ev.Request
		fmt.Fprintf(os.Stdout, "→ %s [%s] %s (%s)\n", r.Time.Format("15:04:05"), r.Box, truncate(r.URL, 60), r.Outcome)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
