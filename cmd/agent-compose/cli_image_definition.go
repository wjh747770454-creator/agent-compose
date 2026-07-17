package main

import "github.com/spf13/cobra"

func newCLIImagesCommand(cli *cliOptions) *cobra.Command {
	return newCLIImageListCommand(cli, "images", "List daemon images")
}

func newCLIImageCommand(cli *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use: "image", Short: "Manage daemon images", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(
		newCLIImageListCommand(cli, "ls", "List daemon images"),
		newCLIImagePullCommand(cli, "pull [image]"),
		newCLIImageBuildCommand(cli, "build [agent...]"),
		newCLIImageRemoveCommand(cli, "rm <image>"),
		newCLIImageInspectCommand(cli),
	)
	return cmd
}

func newCLILegacyImageCommands(cli *cliOptions) []*cobra.Command {
	return []*cobra.Command{
		newCLIImagePullCommand(cli, "pull [image]"),
		newCLIImageBuildCommand(cli, "build [agent...]"),
		newCLIImageRemoveCommand(cli, "rmi <image>"),
	}
}

func newCLIImageListCommand(cli *cliOptions, use, short string) *cobra.Command {
	options := composeImageListOptions{}
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runComposeImageListCommand(cmd, *cli, options)
	}}
	addImageListFlags(cmd, &options)
	return cmd
}

func newCLIImagePullCommand(cli *cliOptions, use string) *cobra.Command {
	options := composeImagePullOptions{}
	cmd := &cobra.Command{Use: use, Short: "Pull an image or all project images", Args: cobra.RangeArgs(0, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return runComposePullCommand(cmd, *cli, options, args)
	}}
	addImagePullFlags(cmd, &options)
	return cmd
}

func newCLIImageBuildCommand(cli *cliOptions, use string) *cobra.Command {
	options := composeImageBuildOptions{}
	cmd := &cobra.Command{Use: use, Short: "Build project agent images", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeBuildCommand(cmd, *cli, options, args)
	}}
	addImageBuildFlags(cmd, &options)
	return cmd
}

func newCLIImageRemoveCommand(cli *cliOptions, use string) *cobra.Command {
	options := composeImageRemoveOptions{}
	cmd := &cobra.Command{Use: use, Short: "Remove an image", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeImageRemoveCommand(cmd, *cli, options, args[0])
	}}
	addImageRemoveFlags(cmd, &options)
	return cmd
}

func newCLIImageInspectCommand(cli *cliOptions) *cobra.Command {
	return &cobra.Command{Use: "inspect <image>", Short: "Inspect an image", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runComposeImageInspectCommand(cmd, *cli, args[0])
	}}
}
