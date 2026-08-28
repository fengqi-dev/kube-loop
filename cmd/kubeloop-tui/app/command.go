package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	tuiruntime "github.com/fengqi-dev/kube-loop/internal/tui/runtime"
)

// NewTUICommand returns the kubeloop cobra command.
func NewTUICommand() *cobra.Command {
	return newTUICommand(buildinfo.Get())
}

func newTUICommand(info buildinfo.Info) *cobra.Command {
	command := &cobra.Command{
		Use:     "kubeloop",
		Short:   "Open the KubeLoop terminal client",
		Version: info.Version,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			if err := registerBundledResources(); err != nil {
				return fmt.Errorf("register bundled resources: %w", err)
			}
			return tuiruntime.Run(signalContext, info)
		},
	}
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetVersionTemplate("kubeloop {{.Version}}\n")
	command.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(command.OutOrStdout(), "kubeloop %s\n", info.Version)
				return err
			},
		},
	)
	return command
}
