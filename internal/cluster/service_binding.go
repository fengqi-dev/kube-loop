package cluster

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/cluster/servicebinding"
	corev1 "k8s.io/api/core/v1"
)

// InterceptPort maps one Service port onto a unique Gateway listen port.
type InterceptPort = servicebinding.InterceptPort

// ServiceInterceptSnapshot stores enough state to restore a Service after intercept.
type ServiceInterceptSnapshot = servicebinding.ServiceInterceptSnapshot

// PreviewServiceSnapshot describes a managed ClusterIP Service that exposes a local process.
type PreviewServiceSnapshot = servicebinding.PreviewServiceSnapshot

func (p *Provider) ApplyServiceIntercept(
	ctx context.Context,
	contextName string,
	snapshot *ServiceInterceptSnapshot,
	interceptID string,
) error {
	client, err := p.client(contextName)
	if err != nil {
		return err
	}
	return servicebinding.ApplyServiceIntercept(ctx, client, snapshot, interceptID)
}

func (p *Provider) RestoreServiceIntercept(
	ctx context.Context,
	contextName string,
	snapshot ServiceInterceptSnapshot,
) error {
	client, err := p.client(contextName)
	if err != nil {
		return err
	}
	return servicebinding.RestoreServiceIntercept(ctx, client, snapshot)
}

func (p *Provider) GetService(
	ctx context.Context, contextName, namespace, name string,
) (*corev1.Service, error) {
	client, err := p.client(contextName)
	if err != nil {
		return nil, err
	}
	return servicebinding.GetService(ctx, client, namespace, name)
}

func (p *Provider) CreatePreviewService(
	ctx context.Context,
	contextName string,
	snapshot PreviewServiceSnapshot,
	previewID string,
) (*corev1.Service, error) {
	client, err := p.client(contextName)
	if err != nil {
		return nil, err
	}
	return servicebinding.CreatePreviewService(ctx, client, snapshot, previewID)
}

func (p *Provider) DeletePreviewService(
	ctx context.Context,
	contextName string,
	snapshot PreviewServiceSnapshot,
) error {
	client, err := p.client(contextName)
	if err != nil {
		return err
	}
	return servicebinding.DeletePreviewService(ctx, client, snapshot)
}

// BuildInterceptPorts derives Service port mappings and allocates Gateway listen ports.
func BuildInterceptPorts(
	service *corev1.Service,
	allocate func(protocol corev1.Protocol) (int32, error),
) ([]InterceptPort, error) {
	return servicebinding.BuildInterceptPorts(service, allocate)
}
