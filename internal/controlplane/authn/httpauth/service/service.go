package service

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
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
	ProviderID, ClientCallback, State, Nonce, PKCEChallenge string
}

type Service struct {
	login     *login.Service
	tokens    *token.Service
	logins    *loginLimiter
}

func New(loginService *login.Service, tokenService *token.Service) (*Service, error) {
	if loginService == nil || tokenService == nil {
		return nil, errors.New("login and token services are required")
	}
	return &Service{login: loginService, tokens: tokenService, logins: newLoginLimiter()}, nil
}

func (service *Service) Anonymous(ctx context.Context, providerID, remoteAddress, deviceID string) (token.Pair, error) {
	if !service.logins.allow(providerID, "anonymous", remoteAddress) {
		return token.Pair{}, ErrRateLimited
	}
	result, err := service.login.AuthenticateAnonymous(ctx, providerID)
	if err != nil {
		return token.Pair{}, ErrInvalidCredentials
	}
	service.logins.success(providerID, "anonymous")
	return service.issue(ctx, result, deviceID)
}

func (service *Service) issue(ctx context.Context, result login.ExchangeResult, deviceID string) (token.Pair, error) {
	pair, err := service.tokens.Issue(ctx, result.Principal, deviceID)
	if err != nil {
		return token.Pair{}, ErrTokenUnavailable
	}
	return pair, nil
}

func (service *Service) Start(ctx context.Context, request StartRequest) (login.BeginResult, error) {
	result, err := service.login.Begin(ctx, login.BeginRequest{
		ProviderID: request.ProviderID, ClientCallback: request.ClientCallback,
		ClientState: request.State, Nonce: request.Nonce, PKCEChallenge: request.PKCEChallenge,
	})
	if err != nil {
		return login.BeginResult{}, ErrInvalidLoginRequest
	}
	return result, nil
}

func (service *Service) Callback(ctx context.Context, providerID, code, state string) (string, error) {
	result, err := service.login.CompleteCallback(ctx, login.CallbackRequest{ProviderID: providerID, UpstreamCode: code, UpstreamState: state})
	if err != nil {
		return "", ErrInvalidLoginRequest
	}
	return result.RedirectURL, nil
}

func (service *Service) Exchange(ctx context.Context, code, verifier, deviceID string) (token.Pair, error) {
	result, err := service.login.Exchange(ctx, code, verifier)
	if err != nil {
		return token.Pair{}, ErrInvalidExchangeCode
	}
	return service.issue(ctx, result, deviceID)
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
