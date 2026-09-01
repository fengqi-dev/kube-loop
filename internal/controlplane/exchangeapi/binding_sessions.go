package exchangeapi

import (
	"errors"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
)

func (handler *Service) bindingSessions() (*trafficbindingclient.Manager, error) {
	provider, ok := handler.resources.(interface {
		BindingManager() *trafficbindingclient.Manager
	})
	if !ok || provider.BindingManager() == nil {
		return nil, errors.New("exchange TrafficBinding Session storage is unavailable")
	}
	return provider.BindingManager(), nil
}

func exchangeDocument(
	binding *trafficv1alpha1.TrafficBinding,
	session sessionapi.ActiveSession,
) Document {
	service := ""
	if binding.Spec.Target != nil {
		service = binding.Spec.Target.Name
	}
	return Document{
		ID: binding.Spec.TaskID, SessionID: binding.Spec.SessionID,
		Namespace: binding.Namespace, State: trafficsession.State(binding),
		Service: service, ClusterIP: binding.Spec.ClusterIP,
		Ports: trafficsession.Ports(binding), LocalTargets: trafficsession.LocalTargets(binding),
		CreatedAt: binding.CreationTimestamp.Time, UpdatedAt: trafficsession.UpdatedAt(binding),
		ExpiresAt: session.ExpiresAt.UTC(),
	}
}

func ownedExchange(
	binding *trafficv1alpha1.TrafficBinding,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) bool {
	return trafficsession.Owned(
		binding, identity.Subject, session.ID, session.Namespace,
	) && binding.Spec.Mode == trafficv1alpha1.TrafficBindingModeExchange
}
