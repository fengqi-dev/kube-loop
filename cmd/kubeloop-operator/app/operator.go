package app

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/buildinfo"
	"github.com/fengqi-dev/kube-loop/internal/controller"
)

var (
	Scheme   = runtime.NewScheme()
	SetupLog = ctrl.Log.WithName("setup")
)

type operatorOptions struct {
	metricsAddress        string
	metricsCertificateDir string
	metricsCertificate    string
	metricsKey            string
	webhookCertificateDir string
	webhookCertificate    string
	webhookKey            string
	probeAddress          string
	leaderElection        bool
	secureMetrics         bool
	enableHTTP2           bool
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(Scheme))
	utilruntime.Must(trafficv1alpha1.AddToScheme(Scheme))
	// +kubebuilder:scaffold:scheme
}

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
			return runOperator(signalContext, operatorOptionsFrom(config), zapOptions, info)
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
		"config": "operator.config-file", "metrics-bind-address": "operator.metrics-bind-address",
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

func operatorOptionsFrom(config *viper.Viper) operatorOptions {
	return operatorOptions{
		metricsAddress:        config.GetString("operator.metrics-bind-address"),
		metricsCertificateDir: config.GetString("operator.metrics-cert-path"),
		metricsCertificate:    config.GetString("operator.metrics-cert-name"),
		metricsKey:            config.GetString("operator.metrics-cert-key"),
		webhookCertificateDir: config.GetString("operator.webhook-cert-path"),
		webhookCertificate:    config.GetString("operator.webhook-cert-name"),
		webhookKey:            config.GetString("operator.webhook-cert-key"),
		probeAddress:          config.GetString("operator.health-probe-bind-address"),
		leaderElection:        config.GetBool("operator.leader-elect"),
		secureMetrics:         config.GetBool("operator.metrics-secure"),
		enableHTTP2:           config.GetBool("operator.enable-http2"),
	}
}

func runOperator(ctx context.Context, options operatorOptions, zapOptions zap.Options, info buildinfo.Info) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	tlsOptions := []func(*tls.Config){}
	if !options.enableHTTP2 {
		tlsOptions = append(tlsOptions, func(config *tls.Config) {
			SetupLog.Info("Disabling HTTP/2")
			config.NextProtos = []string{"http/1.1"}
		})
	}
	webhookOptions := webhook.Options{TLSOpts: tlsOptions}
	if options.webhookCertificateDir != "" {
		webhookOptions.CertDir = options.webhookCertificateDir
		webhookOptions.CertName = options.webhookCertificate
		webhookOptions.KeyName = options.webhookKey
	}
	metricsOptions := metricsserver.Options{
		BindAddress: options.metricsAddress, SecureServing: options.secureMetrics, TLSOpts: tlsOptions,
	}
	if options.secureMetrics {
		metricsOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if options.metricsCertificateDir != "" {
		metricsOptions.CertDir = options.metricsCertificateDir
		metricsOptions.CertName = options.metricsCertificate
		metricsOptions.KeyName = options.metricsKey
	}
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes REST configuration: %w", err)
	}
	manager, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: Scheme, Metrics: metricsOptions, WebhookServer: webhook.NewServer(webhookOptions),
		HealthProbeBindAddress: options.probeAddress, LeaderElection: options.leaderElection,
		LeaderElectionID: "69d30dbe.kubeloop.io",
	})
	if err != nil {
		return fmt.Errorf("start manager: %w", err)
	}
	if err := (&controller.TrafficBindingReconciler{
		Client: manager.GetClient(), Scheme: manager.GetScheme(),
		Recorder: manager.GetEventRecorder("trafficbinding-controller"),
	}).SetupWithManager(manager); err != nil {
		return fmt.Errorf("create TrafficBinding controller: %w", err)
	}
	// +kubebuilder:scaffold:builder
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("set up health check: %w", err)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("set up readiness check: %w", err)
	}
	SetupLog.Info("Starting manager", "Version", info.Version, "Commit", info.Commit)
	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("run manager: %w", err)
	}
	return nil
}
