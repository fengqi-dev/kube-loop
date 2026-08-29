package exchangeapi

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionroute"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.RemoteTaskEndpoints {
	return controlplane.RemoteTaskEndpoints{
		Create: sessionroute.WithSession(handler.sessions, handler.create),
		Get:    sessionroute.WithTask(handler.sessions, handler.get),
		Pause:  sessionroute.WithTask(handler.sessions, handler.pause),
		Resume: sessionroute.WithTask(handler.sessions, handler.resume),
		Delete: sessionroute.WithTask(handler.sessions, handler.delete),
		Stop:   sessionroute.WithTask(handler.sessions, handler.pause),
	}
}
