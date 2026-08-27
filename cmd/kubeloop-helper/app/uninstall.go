package app

import (
	"github.com/spf13/cobra"
)

func newUninstallCommand(dependencies commandDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the privileged helper service",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if dependencies.uninstall == nil {
				return unavailable("uninstall")
			}
			return dependencies.uninstall()
		},
	}
}
