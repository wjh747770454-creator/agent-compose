package main

import "github.com/spf13/cobra"

func newCLIConfigCommand(cli *cliOptions) *cobra.Command {
	options := composeConfigOptions{}
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and print normalized compose config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runComposeConfigCommand(cmd, *cli, options)
		},
	}
	cmd.Flags().BoolVar(&options.Quiet, "quiet", false, "Only validate config")
	return cmd
}
