package main

import "github.com/spf13/cobra"

type elevatedOptions struct {
	operation string
	request   string
	result    string
}

func newElevatedCommand(dependencies commandDependencies) *cobra.Command {
	options := elevatedOptions{}
	command := &cobra.Command{
		Use:    "elevated",
		Short:  "Execute an internal privileged operation",
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return requireValues(
				requiredValue{name: "operation", value: options.operation},
				requiredValue{name: "request", value: options.request},
				requiredValue{name: "result", value: options.result},
			)
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			if dependencies.elevated == nil {
				return unavailable("elevated")
			}
			return dependencies.elevated(options)
		},
	}
	command.Flags().StringVar(&options.operation, "operation", "", "elevated operation")
	command.Flags().StringVar(&options.request, "request", "", "elevated request file")
	command.Flags().StringVar(&options.result, "result", "", "elevated result file")
	markFlagsRequired(command, "operation", "request", "result")
	return command
}
