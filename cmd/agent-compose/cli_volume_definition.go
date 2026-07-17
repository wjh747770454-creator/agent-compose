package main

import "github.com/spf13/cobra"

func newCLIVolumeCommand(cli *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "volume", Short: "Manage daemon volumes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	listOptions := composeVolumeListOptions{}
	listCmd := &cobra.Command{Use: "ls", Short: "List daemon volumes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runComposeVolumeListCommand(cmd, *cli, listOptions)
	}}
	addVolumeListFlags(listCmd, &listOptions)
	listCmd.Flags().BoolVar(&listOptions.Verbose, "verbose", false, "Show the full project id")
	createOptions := composeVolumeCreateOptions{}
	createCmd := &cobra.Command{Use: "create <name>", Short: "Create a daemon volume", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeVolumeCreateCommand(cmd, *cli, createOptions, args[0])
	}}
	addVolumeCreateFlags(createCmd, &createOptions)
	inspectCmd := &cobra.Command{Use: "inspect <name>", Short: "Inspect a daemon volume", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeVolumeInspectCommand(cmd, *cli, args[0])
	}}
	removeOptions := composeVolumeRemoveOptions{}
	removeCmd := &cobra.Command{Use: "rm <name> [<name N>]", Aliases: []string{"remove"}, Short: "Remove one or more daemon volumes", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeVolumeRemoveCommand(cmd, *cli, removeOptions, args)
	}}
	addVolumeRemoveFlags(removeCmd, &removeOptions)
	pruneOptions := composeVolumePruneOptions{}
	pruneCmd := &cobra.Command{Use: "prune", Short: "Prune unused daemon volumes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runComposeVolumePruneCommand(cmd, *cli, pruneOptions)
	}}
	addVolumePruneFlags(pruneCmd, &pruneOptions)
	cmd.AddCommand(listCmd, createCmd, inspectCmd, removeCmd, pruneCmd)
	return cmd
}
