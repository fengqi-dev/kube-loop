package app

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func newRunCommand(dependencies commandDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   runCommandName,
		Short: "Run the privileged helper service",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if dependencies.run == nil {
				return unavailable(runCommandName)
			}
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			return dependencies.run(signalContext)
		},
	}
}
