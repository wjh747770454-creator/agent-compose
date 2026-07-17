package main

import "github.com/spf13/cobra"

func newCLISchedulerCommand(cli *cliOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scheduler",
		Short: "Run, inspect, and operate project schedulers, runs, logs, and triggers",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	runOptions := composeSchedulerTriggerOptions{}
	runCmd := &cobra.Command{
		Use: "run <agent>", Short: "Run a scheduler main function", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerMainCommand(cmd, *cli, runOptions, args[0])
		},
	}
	addComposeSchedulerExecutionFlags(runCmd, &runOptions)

	listOptions := composeSchedulerListOptions{}
	listCmd := &cobra.Command{
		Use: "ls [agent]", Short: "List project scheduler triggers", Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerListCommand(cmd, *cli, listOptions, args)
		},
	}
	listCmd.Flags().BoolVar(&listOptions.Verbose, "verbose", false, "Show full scheduler and trigger IDs")

	triggerOptions := composeSchedulerTriggerOptions{}
	triggerCmd := &cobra.Command{
		Use: "trigger <agent> <trigger>", Short: "Manually run a scheduler trigger", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerTriggerCommand(cmd, *cli, triggerOptions, args[0], args[1])
		},
	}
	addComposeSchedulerExecutionFlags(triggerCmd, &triggerOptions)

	runsOptions := composeSchedulerRunsOptions{}
	runsCmd := &cobra.Command{
		Use: "runs [scheduler]", Short: "List project scheduler runs", Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerRunsCommand(cmd, *cli, runsOptions, args)
		},
	}
	runsCmd.Flags().StringVar(&runsOptions.AgentName, "agent", "", "Filter by agent name or id")
	runsCmd.Flags().StringVar(&runsOptions.Trigger, "trigger", "", "Filter by trigger name or id")
	runsCmd.Flags().StringVar(&runsOptions.Status, "status", "", "Filter by run status")
	runsCmd.Flags().Uint32Var(&runsOptions.Limit, "limit", 20, "Maximum runs to show")

	logsOptions := composeSchedulerLogsOptions{}
	logsCmd := &cobra.Command{
		Use: "logs [run]", Short: "Print scheduler run logs", Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerLogsCommand(cmd, *cli, logsOptions, args)
		},
	}
	logsCmd.Flags().StringVar(&logsOptions.AgentName, "agent", "", "Filter by agent name or id")
	logsCmd.Flags().StringVar(&logsOptions.Trigger, "trigger", "", "Filter by trigger name or id")
	logsCmd.Flags().StringVar(&logsOptions.RunID, "run", "", "Filter by scheduler run id")
	logsCmd.Flags().IntVarP(&logsOptions.Tail, "tail", "n", -1, "Show the last N log events")

	stopOptions := composeSchedulerStopOptions{}
	stopCmd := &cobra.Command{
		Use: "stop <run>", Short: "Stop an active scheduler run", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerStopCommand(cmd, *cli, stopOptions, args[0])
		},
	}
	stopCmd.Flags().StringVar(&stopOptions.Reason, "reason", "", "Reason recorded for the canceled run")

	inspectCmd := &cobra.Command{
		Use: "inspect <name-or-id> [trigger]", Short: "Inspect a scheduler, trigger, or scheduler run", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSchedulerInspectCommand(cmd, *cli, args)
		},
	}
	cmd.AddCommand(listCmd, runCmd, triggerCmd, runsCmd, logsCmd, stopCmd, inspectCmd)
	return cmd
}
