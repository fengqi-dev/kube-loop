package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	kruntime "k8s.io/apimachinery/pkg/runtime"
	kubernetesclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
)

type Provider struct {
	base   *rest.Config
	client kubernetesclient.Interface
	config Config
}

func NewInCluster(config Config) (*Provider, error) {
	base, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	return NewForRESTConfig(base, config)
}

func NewForRESTConfig(base *rest.Config, config Config) (*Provider, error) {
	if base == nil {
		return nil, errors.New("kubernetes REST configuration is required")
	}
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	baseConfig := rest.CopyConfig(base)
	applyDefaults(baseConfig, normalized)
	baseConfig.Impersonate = rest.ImpersonationConfig{}
	client, err := kubernetesclient.NewForConfig(baseConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	return &Provider{base: baseConfig, client: client, config: normalized}, nil
}

func (provider *Provider) RESTConfigFor(subject authorization.Subject) (*rest.Config, error) {
	if provider == nil || provider.base == nil {
		return nil, errors.New("kubernetes provider is unavailable")
	}
	config := rest.CopyConfig(provider.base)
	config.Impersonate = rest.ImpersonationConfig{}
	if !provider.config.Impersonation.Enabled {
		return config, nil
	}
	subject.ID = strings.TrimSpace(subject.ID)
	if !safeValue(subject.ID, 256) {
		return nil, errors.New("authenticated identity ID is invalid for Kubernetes impersonation")
	}
	config.Impersonate.UserName = provider.config.Impersonation.UsernamePrefix + subject.ID
	groups := make(map[string]struct{})
	for _, identityGroup := range subject.Groups {
		for _, group := range provider.config.Impersonation.GroupMappings[identityGroup] {
			group = strings.TrimSpace(group)
			groups[group] = struct{}{}
		}
	}
	config.Impersonate.Groups = make([]string, 0, len(groups))
	for group := range groups {
		config.Impersonate.Groups = append(config.Impersonate.Groups, group)
	}
	slices.Sort(config.Impersonate.Groups)
	return config, nil
}

func (provider *Provider) ClientFor(
	subject authorization.Subject,
) (kubernetesclient.Interface, error) {
	if provider == nil {
		return nil, errors.New("kubernetes provider is unavailable")
	}
	if !provider.config.Impersonation.Enabled {
		return provider.client, nil
	}
	config, err := provider.RESTConfigFor(subject)
	if err != nil {
		return nil, err
	}
	client, err := kubernetesclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create impersonating Kubernetes client: %w", err)
	}
	return client, nil
}

// SystemClient returns the Control Plane ServiceAccount client. It is reserved for
// compensating actions that must remain possible after a user's authorization
// lease has expired or been revoked.
func (provider *Provider) SystemClient() (kubernetesclient.Interface, error) {
	if provider == nil || provider.client == nil {
		return nil, errors.New("kubernetes provider is unavailable")
	}
	return provider.client, nil
}

// SystemRESTConfig returns a copy of the Control Plane ServiceAccount REST
// configuration. It is used for internal APIs such as TrafficBinding that are
// never exposed as user-selected Kubernetes resources.
func (provider *Provider) SystemRESTConfig() (*rest.Config, error) {
	if provider == nil || provider.base == nil {
		return nil, errors.New("kubernetes provider is unavailable")
	}
	config := rest.CopyConfig(provider.base)
	config.Impersonate = rest.ImpersonationConfig{}
	return config, nil
}

func (provider *Provider) Check(ctx context.Context) error {
	if provider == nil || provider.client == nil {
		return errors.New("kubernetes provider is unavailable")
	}
	if _, err := provider.client.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw(); err != nil {
		return fmt.Errorf("check Kubernetes API Server: %w", err)
	}
	return nil
}

func applyDefaults(config *rest.Config, normalized Config) {
	config.Timeout = normalized.Timeout
	config.QPS = normalized.QPS
	config.Burst = normalized.Burst
	config.UserAgent = normalized.UserAgent
	config.ContentType = kruntime.ContentTypeJSON
	config.AcceptContentTypes = kruntime.ContentTypeJSON
}
