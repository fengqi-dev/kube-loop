package httpauth

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/httpauth/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type LocalAuthenticator func(context.Context, string, []byte, string, string) (storage.Principal, error)
type Option = service.Option

func WithLocalAuthenticator(authenticator LocalAuthenticator) service.Option {
	return service.WithLocalAuthenticator(service.LocalAuthenticator(authenticator))
}

type Service = service.Service

func New(loginService *login.Service, tokenService *token.Service, options ...service.Option) (*service.Service, error) {
	return service.New(loginService, tokenService, options...)
}
