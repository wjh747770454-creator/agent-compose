package main

import "github.com/spf13/cobra"

func newCLIDaemonCommand(runDaemon daemonRunner) *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Start the agent-compose daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd.Context())
		},
	}
}
