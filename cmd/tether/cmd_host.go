package main

import (
	"errors"

	"github.com/spf13/cobra"
)

func newHostCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "host",
		Short: "Run the desktop-side daemon that opens browsers and relays loopback callbacks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("host: not yet implemented")
		},
	}
	return c
}
