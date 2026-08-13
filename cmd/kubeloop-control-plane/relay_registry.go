package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"k8s.io/client-go/kubernetes"
)

type relayRegistryOptions struct {
	ListenAddress       string
	CertificateFile     string
	PrivateKeyFile      string
	ClientCAFile        string
	AuthenticationMode  string
	TokenAudience       string
	TrustDomain         string
	Namespace           string
	ServiceAccount      string
	AllowedHosts        string
	PublicURL           string
	LeaseDuration       time.Duration
	HeartbeatAfter      time.Duration
	KeyGeneration       uint64
	KeyValidity         time.Duration
	TicketKeyID         string
	TicketSigningKey    ed25519.PrivateKey
	KubernetesClient    kubernetes.Interface
	Context             context.Context
	ControlPlanePodName string
	Logger              *slog.Logger
}

type relayRegistryRuntime struct {
	listenAddress      string
	registry           *relayregistry.Registry
	handler            *relayregistry.HTTPHandler
	authenticator      relayregistry.Authenticator
	tlsConfig          *tls.Config
	allocationTopology map[string]string
}

func newRelayRegistryRuntime(options relayRegistryOptions) (*relayRegistryRuntime, error) {
	options.ListenAddress = strings.TrimSpace(options.ListenAddress)
	if options.ListenAddress == "" {
		return nil, errors.New("Relay Registry listen address is required")
	}
	if options.KeyGeneration == 0 || options.KeyValidity < relayticket.MaximumLifetime ||
		len(options.TicketSigningKey) != ed25519.PrivateKeySize {
		return nil, errors.New("Relay Registry key configuration is invalid")
	}
	now := time.Now().UTC()
	publicKey := options.TicketSigningKey.Public().(ed25519.PublicKey)
	keys, err := relaycontrol.NewVerificationKeySet(
		options.KeyGeneration,
		map[string]ed25519.PublicKey{strings.TrimSpace(options.TicketKeyID): publicKey},
		now.Add(-relayticket.MaximumLifetime), now.Add(options.KeyValidity),
	)
	if err != nil {
		return nil, err
	}
	allowedHosts, err := registryAllowedHosts(options.AllowedHosts, options.PublicURL)
	if err != nil {
		return nil, err
	}
	endpointPolicy, err := relayregistry.EndpointHostPolicy(allowedHosts...)
	if err != nil {
		return nil, err
	}
	registry, err := relayregistry.New(relayregistry.Config{
		LeaseDuration: options.LeaseDuration, HeartbeatAfter: options.HeartbeatAfter,
		TicketIssuer: options.PublicURL, VerificationKeys: keys, EndpointPolicy: endpointPolicy,
	})
	if err != nil {
		return nil, err
	}
	topologyResolver, err := relayregistry.KubernetesTopologyResolver(options.KubernetesClient, map[string]string{
		"app.kubernetes.io/component": "data-plane",
	})
	if err != nil {
		return nil, err
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	allocationTopology, err := relayregistry.KubernetesPodTopology(
		options.Context, options.KubernetesClient, strings.TrimSpace(options.Namespace), strings.TrimSpace(options.ControlPlanePodName),
	)
	if err != nil {
		return nil, err
	}
	var authenticator relayregistry.Authenticator
	switch strings.ToLower(strings.TrimSpace(options.AuthenticationMode)) {
	case "mtls":
		authenticator, err = relayregistry.NewMTLSAuthenticator(relayregistry.MTLSConfig{
			TrustDomain: strings.TrimSpace(options.TrustDomain), Namespace: strings.TrimSpace(options.Namespace),
			ServiceAccount: strings.TrimSpace(options.ServiceAccount), TopologyResolver: topologyResolver,
		})
	case "tokenreview":
		authenticator, err = relayregistry.NewTokenReviewAuthenticator(options.KubernetesClient, relayregistry.TokenReviewConfig{
			Audience: strings.TrimSpace(options.TokenAudience), TrustDomain: strings.TrimSpace(options.TrustDomain),
			Namespace: strings.TrimSpace(options.Namespace), ServiceAccount: strings.TrimSpace(options.ServiceAccount),
			TopologyResolver: topologyResolver,
		})
	default:
		return nil, errors.New("Relay Registry authentication mode must be mtls or tokenreview")
	}
	if err != nil {
		return nil, err
	}
	handler, err := relayregistry.NewHTTPHandler(registry, authenticator, options.Logger)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := relayregistry.LoadServerTLS(relayregistry.ServerTLSConfig{
		CertificateFile: options.CertificateFile, PrivateKeyFile: options.PrivateKeyFile,
		ClientCAFile:             options.ClientCAFile,
		RequireClientCertificate: strings.EqualFold(strings.TrimSpace(options.AuthenticationMode), "mtls"),
	})
	if err != nil {
		return nil, err
	}
	return &relayRegistryRuntime{
		listenAddress: options.ListenAddress, registry: registry, handler: handler, authenticator: authenticator, tlsConfig: tlsConfig,
		allocationTopology: allocationTopology,
	}, nil
}

func registryAllowedHosts(configured, publicURL string) ([]string, error) {
	if strings.TrimSpace(configured) != "" {
		parts := strings.Split(configured, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				result = append(result, value)
			}
		}
		if len(result) == 0 {
			return nil, errors.New("Relay endpoint allowed hosts are empty")
		}
		return result, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("public URL is required to derive the Relay endpoint host")
	}
	return []string{parsed.Hostname()}, nil
}
