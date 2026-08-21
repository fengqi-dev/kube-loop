package main

import "github.com/spf13/cobra"

func newRunCommand(dependencies commandDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   runCommandName,
		Short: "Run the privileged helper service",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if dependencies.run == nil {
				return unavailable(runCommandName)
			}
			return dependencies.run(command.Context())
		},
	}
}
