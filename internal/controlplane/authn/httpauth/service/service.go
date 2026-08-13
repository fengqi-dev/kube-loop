package service

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

var (
	ErrRateLimited         = errors.New("login attempt limit exceeded")
	ErrInvalidCredentials  = errors.New("credentials were rejected")
	ErrTokenUnavailable    = errors.New("token service is unavailable")
	ErrInvalidLoginRequest = errors.New("login request was rejected")
	ErrInvalidExchangeCode = errors.New("exchange code was rejected")
	ErrInvalidRefreshToken = errors.New("refresh token was rejected")
)

type StartRequest struct {
	ProviderID, ClientID, ClientCallback, State, Nonce, PKCEChallenge, Scope string
}

type Service struct {
	login  *login.Service
	tokens *token.Service
	logins *loginLimiter
	local  LocalAuthenticator
}

type LocalAuthenticator func(context.Context, string, []byte, string, string) (storage.Principal, error)

type Option func(*Service)

func WithLocalAuthenticator(authenticator LocalAuthenticator) Option {
	return func(service *Service) { service.local = authenticator }
}

func New(loginService *login.Service, tokenService *token.Service, options ...Option) (*Service, error) {
	if loginService == nil || tokenService == nil {
		return nil, errors.New("login and token services are required")
	}
	service := &Service{login: loginService, tokens: tokenService, logins: newLoginLimiter()}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (service *Service) Start(ctx context.Context, request StartRequest) (login.BeginResult, error) {
	result, err := service.login.Begin(ctx, login.BeginRequest{
		ProviderID: request.ProviderID, ClientID: request.ClientID, ClientCallback: request.ClientCallback,
		ClientState: request.State, Nonce: request.Nonce, PKCEChallenge: request.PKCEChallenge,
		Scope: request.Scope,
	})
	if err != nil {
		return login.BeginResult{}, ErrInvalidLoginRequest
	}
	return result, nil
}

func (service *Service) StartLocal(ctx context.Context, request StartRequest) (login.LocalBeginResult, error) {
	if service.local == nil {
		return login.LocalBeginResult{}, ErrInvalidLoginRequest
	}
	result, err := service.login.BeginLocal(ctx, login.BeginRequest{
		ProviderID: "local", ClientID: request.ClientID, ClientCallback: request.ClientCallback,
		ClientState: request.State, Nonce: request.Nonce, PKCEChallenge: request.PKCEChallenge,
		Scope: request.Scope,
	})
	if err != nil {
		return login.LocalBeginResult{}, ErrInvalidLoginRequest
	}
	return result, nil
}

func (service *Service) CompleteLocal(
	ctx context.Context,
	transaction, username string,
	password []byte,
	secondFactor, requestID, remoteAddress string,
) (string, error) {
	if service.local == nil || !service.logins.allow("local", "password", remoteAddress) {
		return "", ErrRateLimited
	}
	principal, err := service.local(ctx, username, password, secondFactor, requestID)
	if err != nil {
		return "", ErrInvalidCredentials
	}
	result, err := service.login.CompleteLocal(ctx, transaction, principal.ID)
	if err != nil {
		return "", ErrInvalidLoginRequest
	}
	service.logins.success("local", "password")
	return result.RedirectURL, nil
}

// CompleteLocalPrincipal completes a local authorization transaction for a
// Principal whose existing browser session has already been authenticated by
// the caller. CompleteLocal still validates that the transaction and stored
// Principal belong to the local provider.
func (service *Service) CompleteLocalPrincipal(
	ctx context.Context,
	transaction string,
	principal storage.Principal,
) (string, error) {
	result, err := service.login.CompleteLocal(ctx, transaction, principal.ID)
	if err != nil {
		return "", ErrInvalidLoginRequest
	}
	return result.RedirectURL, nil
}

func (service *Service) Callback(ctx context.Context, providerID, code, state string) (string, error) {
	result, err := service.login.CompleteCallback(ctx, login.CallbackRequest{ProviderID: providerID, UpstreamCode: code, UpstreamState: state})
	if err != nil {
		return "", ErrInvalidLoginRequest
	}
	return result.RedirectURL, nil
}

func (service *Service) Exchange(
	ctx context.Context,
	code, verifier, clientID, redirectURI, deviceID string,
) (token.Pair, string, error) {
	result, err := service.login.Exchange(ctx, code, verifier, clientID, redirectURI)
	if err != nil {
		return token.Pair{}, "", ErrInvalidExchangeCode
	}
	pair, err := service.tokens.IssueOIDC(ctx, result.Principal, deviceID, result.ClientID, result.Nonce)
	if err != nil {
		return token.Pair{}, "", ErrTokenUnavailable
	}
	return pair, result.Scope, nil
}

func (service *Service) Refresh(ctx context.Context, refreshToken string) (token.Pair, error) {
	pair, err := service.tokens.Refresh(ctx, refreshToken)
	if err != nil {
		return token.Pair{}, ErrInvalidRefreshToken
	}
	return pair, nil
}

func (service *Service) Revoke(ctx context.Context, refreshToken string) {
	_ = service.tokens.Revoke(ctx, refreshToken)
}

func (service *Service) Authenticate(ctx context.Context, accessToken string) (token.AccessIdentity, error) {
	return service.tokens.Authenticate(ctx, accessToken)
}

func (service *Service) TokenService() *token.Service { return service.tokens }
