package main

import (
	"encoding/json"
	"fmt"

	"agent-compose/pkg/config"

	"github.com/spf13/cobra"
)

func newCLIVersionCommand(options *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.JSON {
				data, err := json.MarshalIndent(currentBuildInfo(), "", "  ")
				if err != nil {
					return err
				}
				return writeCommandOutput(cmd.OutOrStdout(), append(data, '\n'))
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), config.BuildVersion)
			return err
		},
	}
}
