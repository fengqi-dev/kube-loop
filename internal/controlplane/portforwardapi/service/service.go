package service

import (
	"context"
	"errors"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
)

const TaskType = "port-forward"

type Resolver interface {
	Resolve(
		context.Context,
		controlplaneapi.Identity,
		string,
		Spec,
	) (Target, error)
}

type BindingManager interface {
	Activate(
		context.Context,
		controlplaneapi.Identity,
		sessionapi.ActiveSession,
		string,
		Spec,
		Target,
	) (bool, error)
	Get(context.Context, string, string) (*trafficv1alpha1.TrafficBinding, error)
	List(context.Context, string, string) ([]trafficv1alpha1.TrafficBinding, error)
	Stop(context.Context, string, string) error
	Delete(context.Context, string, string) error
}

type Config struct{}

type Service struct {
	resolver Resolver
	bindings BindingManager
}

func New(
	resolver Resolver,
	bindings BindingManager,
	_ Config,
) (*Service, error) {
	if resolver == nil || bindings == nil {
		return nil, errors.New(
			"port forward target resolver and TrafficBinding manager are required",
		)
	}
	return &Service{
		resolver: resolver,
		bindings: bindings,
	}, nil
}
