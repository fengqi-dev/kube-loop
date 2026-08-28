package runtime

import (
	"context"
	"crypto/tls"
	"fmt"

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

type Options struct {
	MetricsAddress        string
	MetricsCertificateDir string
	MetricsCertificate    string
	MetricsKey            string
	WebhookCertificateDir string
	WebhookCertificate    string
	WebhookKey            string
	ProbeAddress          string
	LeaderElection        bool
	SecureMetrics         bool
	EnableHTTP2           bool
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(Scheme))
	utilruntime.Must(trafficv1alpha1.AddToScheme(Scheme))
	// +kubebuilder:scaffold:scheme
}

func Run(ctx context.Context, options Options, zapOptions zap.Options, info buildinfo.Info) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))
	tlsOptions := []func(*tls.Config){}
	if !options.EnableHTTP2 {
		tlsOptions = append(tlsOptions, func(config *tls.Config) {
			SetupLog.Info("Disabling HTTP/2")
			config.NextProtos = []string{"http/1.1"}
		})
	}
	webhookOptions := webhook.Options{TLSOpts: tlsOptions}
	if options.WebhookCertificateDir != "" {
		webhookOptions.CertDir = options.WebhookCertificateDir
		webhookOptions.CertName = options.WebhookCertificate
		webhookOptions.KeyName = options.WebhookKey
	}
	metricsOptions := metricsserver.Options{
		BindAddress: options.MetricsAddress, SecureServing: options.SecureMetrics, TLSOpts: tlsOptions,
	}
	if options.SecureMetrics {
		metricsOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if options.MetricsCertificateDir != "" {
		metricsOptions.CertDir = options.MetricsCertificateDir
		metricsOptions.CertName = options.MetricsCertificate
		metricsOptions.KeyName = options.MetricsKey
	}
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes REST configuration: %w", err)
	}
	manager, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: Scheme, Metrics: metricsOptions, WebhookServer: webhook.NewServer(webhookOptions),
		HealthProbeBindAddress: options.ProbeAddress, LeaderElection: options.LeaderElection,
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
