package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mwdomino/tether/internal/config"
)

func newBoxCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "box",
		Short: "Manage the boxes whose SSH forwards the host daemon supervises.",
	}
	c.AddCommand(newBoxAddCmd(), newBoxListCmd(), newBoxRemoveCmd())
	return c
}

func newBoxAddCmd() *cobra.Command {
	var (
		configPath string
		sshHost    string
		remotePort int
	)
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a box (SSH destination) for the daemon to keep connected.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if err := cfg.AddBox(config.Box{Name: args[0], SSHHost: sshHost, RemotePort: remotePort}); err != nil {
				return err
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "tether: added box %q -> %s\n", args[0], sshHost)
			fmt.Fprintln(os.Stderr, "tether: run 'tether reload' (or restart the host) to apply.")
			return nil
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "config file path (default ~/.config/tether/config.json)")
	c.Flags().StringVar(&sshHost, "ssh-host", "", "ssh destination / Host alias from ~/.ssh/config (required)")
	c.Flags().IntVar(&remotePort, "remote-port", 0, fmt.Sprintf("loopback port bound on the box (default %d)", config.DefaultRemotePort))
	_ = c.MarkFlagRequired("ssh-host")
	return c
}

func newBoxListCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "list",
		Short: "List configured boxes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if len(cfg.Boxes) == 0 {
				fmt.Fprintln(os.Stderr, "tether: no boxes configured (add one with 'tether box add')")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSSH HOST\tREMOTE PORT")
			for _, b := range cfg.Boxes {
				fmt.Fprintf(w, "%s\t%s\t%d\n", b.Name, b.SSHHost, b.RemotePort)
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "config file path (default ~/.config/tether/config.json)")
	return c
}

func newBoxRemoveCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Remove a box.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if err := cfg.RemoveBox(args[0]); err != nil {
				return err
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "tether: removed box %q\n", args[0])
			return nil
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "config file path (default ~/.config/tether/config.json)")
	return c
}

func resolveConfigPath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	return config.DefaultPath()
}
