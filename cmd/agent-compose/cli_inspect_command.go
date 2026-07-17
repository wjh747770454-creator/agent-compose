package main

import "github.com/spf13/cobra"

func newCLIInspectCommand(cli *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <id>|<project|agent|run|sandbox|image|cache|volume> [name-or-id]",
		Short: "Inspect project, agent, run, sandbox, image, cache, or volume details",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeInspectCommand(cmd, *cli, args)
		},
	}
}
