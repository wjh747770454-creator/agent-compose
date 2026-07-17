package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCLIStatusCommand(options *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Query daemon status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientConfig, err := resolveCLIClientConfig(options.Host)
			if err != nil {
				return err
			}
			body, err := fetchDaemonVersion(cmd.Context(), clientConfig)
			if err != nil {
				return err
			}
			if options.JSON {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(body))
				return err
			}
			return writeDaemonStatusText(cmd.OutOrStdout(), body)
		},
	}
}
