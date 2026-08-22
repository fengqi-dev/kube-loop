package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCommand(commandVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   versionCommandName,
		Short: "Print the helper version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(command.OutOrStdout(), "kubeloop-helper", commandVersion)
			return err
		},
	}
}
