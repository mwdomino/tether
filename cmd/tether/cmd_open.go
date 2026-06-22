package main

import (
	"errors"

	"github.com/spf13/cobra"
)

func newOpenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "open <url>",
		Short: "Send a URL to the desktop host to be opened in a browser.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("open: not yet implemented")
		},
	}
	return c
}
