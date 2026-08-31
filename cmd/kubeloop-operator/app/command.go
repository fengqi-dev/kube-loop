package app

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	operatorruntime "github.com/fengqi-dev/kube-loop/internal/operator/runtime"
)

// NewOperatorCommand returns the kubeloop-operator cobra command.
func NewOperatorCommand() *cobra.Command {
	return newOperatorCommandWithConfig(newOperatorConfigResolver(), buildinfo.Get())
}

func newOperatorCommandWithConfig(config *viper.Viper, info buildinfo.Info) *cobra.Command {
	zapOptions := zap.Options{Development: true}
	command := &cobra.Command{
		Use:     "kubeloop-operator",
		Short:   "Run the KubeLoop TrafficBinding operator",
		Version: info.Version,
		Args:    cobra.NoArgs,
		PreRunE: func(*cobra.Command, []string) error {
			configFile := strings.TrimSpace(config.GetString("operator.config-file"))
			if configFile == "" {
				return nil
			}
			config.SetConfigFile(configFile)
			if err := config.ReadInConfig(); err != nil {
				return fmt.Errorf("read operator configuration: %w", err)
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			signalContext, stopSignals := signal.NotifyContext(
				command.Context(),
				os.Interrupt,
				syscall.SIGTERM,
			)
			defer stopSignals()
			return operatorruntime.Run(signalContext, operatorOptionsFrom(config), zapOptions, info)
		},
	}
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetVersionTemplate("kubeloop-operator {{.Version}}\n")
	command.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				_, err := fmt.Fprintf(command.OutOrStdout(), "kubeloop-operator %s\n", info.Version)
				return err
			},
		},
	)

	flags := command.Flags()
	flags.String("config", "", "optional Operator YAML configuration file")
	flags.String("crd-file", "", "optional TrafficBinding CRD manifest synchronized before startup")
	flags.String("metrics-bind-address", "0", "metrics listen address, or 0 to disable")
	flags.String("health-probe-bind-address", ":8081", "health probe listen address")
	flags.Bool("leader-elect", false, "enable leader election")
	flags.Bool("metrics-secure", true, "serve metrics using HTTPS")
	flags.String("webhook-cert-path", "", "webhook certificate directory")
	flags.String("webhook-cert-name", "tls.crt", "webhook certificate filename")
	flags.String("webhook-cert-key", "tls.key", "webhook private key filename")
	flags.String("metrics-cert-path", "", "metrics certificate directory")
	flags.String("metrics-cert-name", "tls.crt", "metrics certificate filename")
	flags.String("metrics-cert-key", "tls.key", "metrics private key filename")
	flags.Bool("enable-http2", false, "enable HTTP/2 for metrics and webhooks")

	goFlags := flag.NewFlagSet("zap", flag.ContinueOnError)
	zapOptions.BindFlags(goFlags)
	flags.AddGoFlagSet(goFlags)
	bindings := map[string]string{
		"config": "operator.config-file", "crd-file": "operator.crd-file",
		"metrics-bind-address":      "operator.metrics-bind-address",
		"health-probe-bind-address": "operator.health-probe-bind-address", "leader-elect": "operator.leader-elect",
		"metrics-secure": "operator.metrics-secure", "webhook-cert-path": "operator.webhook-cert-path",
		"webhook-cert-name": "operator.webhook-cert-name", "webhook-cert-key": "operator.webhook-cert-key",
		"metrics-cert-path": "operator.metrics-cert-path", "metrics-cert-name": "operator.metrics-cert-name",
		"metrics-cert-key": "operator.metrics-cert-key", "enable-http2": "operator.enable-http2",
	}
	for flagName, key := range bindings {
		if err := config.BindPFlag(key, flags.Lookup(flagName)); err != nil {
			panic(err)
		}
	}
	return command
}

func newOperatorConfigResolver() *viper.Viper {
	config := viper.New()
	config.SetEnvPrefix("KUBELOOP")
	config.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	config.AutomaticEnv()
	return config
}

func operatorOptionsFrom(config *viper.Viper) operatorruntime.Options {
	return operatorruntime.Options{
		CRDFile:               config.GetString("operator.crd-file"),
		MetricsAddress:        config.GetString("operator.metrics-bind-address"),
		MetricsCertificateDir: config.GetString("operator.metrics-cert-path"),
		MetricsCertificate:    config.GetString("operator.metrics-cert-name"),
		MetricsKey:            config.GetString("operator.metrics-cert-key"),
		WebhookCertificateDir: config.GetString("operator.webhook-cert-path"),
		WebhookCertificate:    config.GetString("operator.webhook-cert-name"),
		WebhookKey:            config.GetString("operator.webhook-cert-key"),
		ProbeAddress:          config.GetString("operator.health-probe-bind-address"),
		LeaderElection:        config.GetBool("operator.leader-elect"),
		SecureMetrics:         config.GetBool("operator.metrics-secure"),
		EnableHTTP2:           config.GetBool("operator.enable-http2"),
	}
}
