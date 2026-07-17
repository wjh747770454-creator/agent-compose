package main

import "github.com/spf13/cobra"

func newCLICacheCommand(cli *cliOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "cache", Short: "Manage daemon runtime caches", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	listOptions := composeCacheFilterOptions{}
	listCmd := &cobra.Command{Use: "ls", Short: "List daemon runtime caches", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runComposeCacheListCommand(cmd, *cli, listOptions)
	}}
	addCacheFilterFlags(listCmd, &listOptions)
	inspectCmd := &cobra.Command{Use: "inspect <cache-id>", Short: "Inspect a daemon runtime cache item", Args: cacheInspectArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeCacheInspectCommand(cmd, *cli, args[0])
	}}
	pruneOptions := composeCachePruneOptions{}
	pruneCmd := &cobra.Command{Use: "prune", Short: "Prune daemon runtime caches", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runComposeCachePruneCommand(cmd, *cli, pruneOptions)
	}}
	addCachePruneFlags(pruneCmd, &pruneOptions)
	removeOptions := composeCacheRemoveOptions{}
	removeCmd := &cobra.Command{Use: "rm <cache-id>", Short: "Remove a daemon runtime cache item", Args: cacheRemoveArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeCacheRemoveCommand(cmd, *cli, removeOptions, args[0])
	}}
	addCacheRemoveFlags(removeCmd, &removeOptions)
	cmd.AddCommand(listCmd, inspectCmd, pruneCmd, removeCmd)
	return cmd
}
