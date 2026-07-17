package main

import "github.com/spf13/cobra"

func newCLIStatsCommand(cli *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "stats [sandbox]",
		Short: "Print sandbox resource stats",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStatsCommand(cmd, *cli, args)
		},
	}
}
