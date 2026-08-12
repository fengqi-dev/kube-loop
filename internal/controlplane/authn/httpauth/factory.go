package httpauth

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
)

type Service = service.Service

func New(loginService *login.Service, tokenService *token.Service) (*service.Service, error) {
	return service.New(loginService, tokenService)
}
