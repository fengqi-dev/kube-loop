package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	options "github.com/fengqi-dev/kube-loop/internal/gateway/config"
	gatewayruntime "github.com/fengqi-dev/kube-loop/internal/gateway/runtime"
)

// NewGatewayCommand returns the kubeloop-gateway cobra command.
func NewGatewayCommand() *cobra.Command {
	return newGatewayCommand(buildinfo.Get())
}

func newGatewayCommand(info buildinfo.Info) *cobra.Command {
	configResolver := options.NewConfigResolver()
	command := &cobra.Command{
		Use:     "kubeloop-gateway",
		Short:   "Run the KubeLoop tunnel data plane",
		Version: info.Version,
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			environment, err := options.LoadEnvironmentFrom(configResolver)
			if err != nil {
				return err
			}
			config, err := options.LoadConfig(environment.ConfigFile)
			if err != nil {
				return err
			}
			config, err = options.ApplyOverrides(configResolver, config)
			if err != nil {
				return err
			}
			return gatewayruntime.Run(signalContext, environment, config, info, command.OutOrStdout())
		},
	}
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetVersionTemplate("kubeloop-gateway {{.Version}}\n")
	command.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(command.OutOrStdout(), "kubeloop-gateway %s\n", info.Version)
				return err
			},
		},
	)
	command.Flags().String("config", "", "unified KubeLoop YAML configuration file")
	command.Flags().String("listen", "", "Gateway HTTP listen address")
	command.Flags().String("relay-control-plane-url", "", "Relay Registry control plane URL")
	command.Flags().String("relay-endpoint", "", "advertised Relay endpoint")
	command.Flags().String("log-level", "", "log level: debug, info, warn, or error")
	bindings := map[string]string{
		"config": "gateway.config-file", "listen": "gateway.http.listen",
		"relay-control-plane-url": "gateway.relay.control-plane-url",
		"relay-endpoint":          "gateway.relay.endpoint", "log-level": "gateway.log-level",
	}
	for flagName, key := range bindings {
		if err := configResolver.BindPFlag(key, command.Flags().Lookup(flagName)); err != nil {
			panic(err)
		}
	}
	return command
}
