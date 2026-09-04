package previewapi

import "github.com/fengqi-dev/kube-loop/internal/controlplane"

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.RemoteTaskEndpoints {
	return handler.Handlers.Endpoints()
}
