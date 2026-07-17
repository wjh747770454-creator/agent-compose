package main

import "github.com/spf13/cobra"

func newCLIExecCommand(cli *cliOptions) *cobra.Command {
	options := composeExecOptions{}
	cmd := &cobra.Command{
		Use:   "exec <sandbox> (--command <shell-command> | --prompt <prompt> | -- <command> [args...])",
		Short: "Execute a command in a running sandbox",
		Args:  composeExecArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeExecCommand(cmd, *cli, options, args)
		},
	}
	cmd.Flags().StringVar(&options.RunID, "run", "", "Deprecated target selection by run; use exec <sandbox>")
	cmd.Flags().StringVar(&options.Command, "command", "", "Shell command to execute in the sandbox")
	cmd.Flags().StringVar(&options.Prompt, "prompt", "", "Prompt the sandbox agent and attach to the response")
	cmd.Flags().BoolVarP(&options.Interactive, "interactive", "i", false, "Attach stdin to the sandbox command")
	cmd.Flags().BoolVarP(&options.TTY, "tty", "t", false, "Allocate a TTY for interactive exec")
	cmd.Flags().StringVar(&options.Cwd, "cwd", "", "Guest working directory")
	return cmd
}
