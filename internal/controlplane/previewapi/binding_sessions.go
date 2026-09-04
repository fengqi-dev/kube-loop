package previewapi

import (
	"errors"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
)

func (handler *Service) bindingSessions() (*trafficbindingclient.Manager, error) {
	provider, ok := handler.resources.(interface {
		BindingManager() *trafficbindingclient.Manager
	})
	if !ok || provider.BindingManager() == nil {
		return nil, errors.New("preview TrafficBinding Session storage is unavailable")
	}
	return provider.BindingManager(), nil
}

func previewDocument(binding *trafficv1alpha1.TrafficBinding) Document {
	name := ""
	if binding.Spec.Preview != nil {
		name = binding.Spec.Preview.ServiceName
	}
	return Document{
		ID: binding.Spec.TaskID, SessionID: binding.Spec.SessionID,
		Namespace: binding.Namespace, State: trafficsession.State(binding),
		Name: name, ClusterIP: binding.Status.ServiceClusterIP,
		Ports: trafficsession.Ports(binding), LocalTargets: trafficsession.LocalTargets(binding),
		CreatedAt: binding.CreationTimestamp.Time, UpdatedAt: trafficsession.UpdatedAt(binding),
	}
}
