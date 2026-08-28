package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	options "github.com/fengqi-dev/kube-loop/internal/controlplane/config"
	controlplaneruntime "github.com/fengqi-dev/kube-loop/internal/controlplane/runtime"
	"github.com/fengqi-dev/kube-loop/internal/logging"
)

// NewControlPlaneCommand returns the kubeloop-control-plane cobra command.
func NewControlPlaneCommand() *cobra.Command {
	return newControlPlaneCommand(buildinfo.Get(), buildinfo.ControlPlaneProtocol())
}

func newControlPlaneCommand(
	info buildinfo.Info,
	protocol buildinfo.ProtocolRange,
) *cobra.Command {
	configResolver := options.NewConfigResolver()
	command := &cobra.Command{
		Use:     "kubeloop-control-plane",
		Short:   "Run the KubeLoop control plane",
		Version: info.Version,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			environment := options.LoadEnvironmentFrom(configResolver)
			configPath := options.Path(configResolver)
			config, err := options.LoadConfig(configPath)
			if err != nil {
				return err
			}
			config, err = options.ApplyOverrides(configResolver, config)
			if err != nil {
				return err
			}
			parsedLogLevel, err := logging.ParseLevel(config.Document.Logging.Level)
			if err != nil {
				return fmt.Errorf("invalid log level: %w", err)
			}
			logger := slog.New(logging.WithContext(
				slog.NewJSONHandler(command.OutOrStdout(), &slog.HandlerOptions{Level: parsedLogLevel}),
			)).With("component", "control-plane")
			runContext, stop := context.WithCancel(signalContext)
			defer stop()
			return controlplaneruntime.Run(runContext, stop, config, environment, info, protocol, logger)
		},
	}
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetVersionTemplate("kubeloop-control-plane {{.Version}}\n")
	command.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(command.OutOrStdout(), "kubeloop-control-plane %s\n", info.Version)
				return err
			},
		},
	)
	command.Flags().String("config", "", "unified KubeLoop YAML configuration file")
	command.Flags().String("listen", "", "Control Plane API listen address")
	command.Flags().String("public-url", "", "public Control Plane URL")
	command.Flags().String("log-level", "", "log level: debug, info, warn, or error")
	bindings := map[string]string{
		"config": "control-plane.config-file", "listen": "control-plane.api.listen",
		"public-url": "control-plane.api.public-url", "log-level": "control-plane.logging.level",
	}
	for flagName, key := range bindings {
		if err := configResolver.BindPFlag(key, command.Flags().Lookup(flagName)); err != nil {
			panic(err)
		}
	}
	return command
}
