package fileapi

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionroute"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.FileTransferEndpoints {
	return controlplane.FileTransferEndpoints{
		Create: sessionroute.WithSession(handler.sessions, handler.create),
		Get:    sessionroute.WithTask(handler.sessions, handler.get),
		Stream: sessionroute.WithTask(handler.sessions, handler.stream),
	}
}
