package login

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

const (
	defaultAttemptTTL  = 5 * time.Minute
	defaultExchangeTTL = time.Minute
)

var (
	ErrInvalidRequest    = errors.New("invalid login request")
	ErrUnknownProvider   = errors.New("unknown authentication provider")
	ErrExpiredOrReplayed = errors.New("login transaction expired or already used")
	ErrPKCEVerification  = errors.New("PKCE verification failed")
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type Config struct {
	AttemptTTL  time.Duration
	ExchangeTTL time.Duration
	Clients     []Client
	Now         func() time.Time
	Random      func([]byte) error
}

type Service struct {
	providers   *authn.Registry
	store       Store
	attemptTTL  time.Duration
	exchangeTTL time.Duration
	clients     map[string]Client
	now         func() time.Time
	random      func([]byte) error
}

type BeginRequest struct {
	ProviderID     string
	ClientID       string
	ClientCallback string
	ClientState    string
	Nonce          string
	PKCEChallenge  string
	Scope          string
}

type BeginResult struct {
	AuthorizationURL string
	ExpiresAt        time.Time
}

type LocalBeginResult struct {
	Transaction string
	ExpiresAt   time.Time
}

type CallbackRequest struct {
	ProviderID    string
	UpstreamCode  string
	UpstreamState string
}

type CallbackResult struct {
	RedirectURL string
}

type ExchangeResult struct {
	Principal storage.Principal
	ClientID  string
	Scope     string
	Nonce     string
}

const DefaultDesktopClientID = "kubeloop-desktop"

type Client struct {
	ID            string
	RedirectURIs  []string
	AllowLoopback bool
	Scopes        []string
}

func New(providers *authn.Registry, store Store, config Config) (*Service, error) {
	if providers == nil || store == nil {
		return nil, errors.New("login providers and store are required")
	}
	if config.AttemptTTL <= 0 {
		config.AttemptTTL = defaultAttemptTTL
	}
	if config.ExchangeTTL <= 0 {
		config.ExchangeTTL = defaultExchangeTTL
	}
	if config.AttemptTTL > 15*time.Minute || config.ExchangeTTL > 5*time.Minute {
		return nil, errors.New("login transaction TTL exceeds security limit")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = func(buffer []byte) error {
			_, err := rand.Read(buffer)
			return err
		}
	}
	clients := make(map[string]Client, len(config.Clients))
	for _, client := range config.Clients {
		client.ID = strings.TrimSpace(client.ID)
		if client.ID == "" || len(client.ID) > 128 || len(client.Scopes) == 0 {
			return nil, errors.New("OAuth client ID and scopes are required")
		}
		if _, exists := clients[client.ID]; exists {
			return nil, errors.New("OAuth client ID is duplicated")
		}
		scopes, err := normalizeScopes(strings.Join(client.Scopes, " "), nil)
		if err != nil {
			return nil, err
		}
		client.Scopes = strings.Fields(scopes)
		for index, value := range client.RedirectURIs {
			callback, callbackErr := validateConfiguredCallback(value)
			if callbackErr != nil {
				return nil, callbackErr
			}
			client.RedirectURIs[index] = callback
		}
		if len(client.RedirectURIs) == 0 && !client.AllowLoopback {
			return nil, errors.New("OAuth client redirect URI is required")
		}
		clients[client.ID] = client
	}
	return &Service{
		providers: providers, store: store,
		attemptTTL: config.AttemptTTL, exchangeTTL: config.ExchangeTTL,
		clients: clients, now: config.Now, random: config.Random,
	}, nil
}

func (service *Service) Begin(ctx context.Context, request BeginRequest) (BeginResult, error) {
	client, callback, scope, err := service.validateAuthorizationRequest(&request)
	if err != nil {
		return BeginResult{}, err
	}
	_ = client
	provider, ok := service.providers.Provider(request.ProviderID)
	if !ok {
		return BeginResult{}, ErrUnknownProvider
	}
	authorizationCodeProvider, ok := provider.(authn.AuthorizationCodeProvider)
	if !ok {
		return BeginResult{}, ErrInvalidRequest
	}
	upstreamState, err := service.randomValue()
	if err != nil {
		return BeginResult{}, errors.New("generate login state")
	}
	upstreamVerifier, err := service.randomValue()
	if err != nil {
		return BeginResult{}, errors.New("generate upstream PKCE verifier")
	}
	upstreamVerifierHash := sha256.Sum256([]byte(upstreamVerifier))
	upstreamChallenge := base64.RawURLEncoding.EncodeToString(upstreamVerifierHash[:])
	authorizationURL, err := authorizationCodeProvider.AuthorizationURL(
		upstreamState, request.Nonce, upstreamChallenge,
	)
	if err != nil {
		return BeginResult{}, fmt.Errorf("create upstream authorization request: %w", err)
	}
	attempt := service.newAttempt(request, callback, scope, upstreamState, upstreamVerifier)
	if err := service.store.AuthTransactions().CreateAttempt(ctx, attempt); err != nil {
		return BeginResult{}, errors.New("persist login transaction")
	}
	return BeginResult{AuthorizationURL: authorizationURL, ExpiresAt: attempt.ExpiresAt}, nil
}

func (service *Service) BeginLocal(ctx context.Context, request BeginRequest) (LocalBeginResult, error) {
	request.ProviderID = "local"
	_, callback, scope, err := service.validateAuthorizationRequest(&request)
	if err != nil {
		return LocalBeginResult{}, err
	}
	transaction, err := service.randomValue()
	if err != nil {
		return LocalBeginResult{}, errors.New("generate local login transaction")
	}
	attempt := service.newAttempt(request, callback, scope, transaction, "local")
	if err := service.store.AuthTransactions().CreateAttempt(ctx, attempt); err != nil {
		return LocalBeginResult{}, errors.New("persist local login transaction")
	}
	return LocalBeginResult{Transaction: transaction, ExpiresAt: attempt.ExpiresAt}, nil
}

func (service *Service) validateAuthorizationRequest(request *BeginRequest) (Client, *url.URL, string, error) {
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.ClientState = strings.TrimSpace(request.ClientState)
	request.Nonce = strings.TrimSpace(request.Nonce)
	request.PKCEChallenge = strings.TrimSpace(request.PKCEChallenge)
	client, ok := service.clients[request.ClientID]
	if !ok {
		return Client{}, nil, "", ErrInvalidRequest
	}
	callback, err := validateClientCallback(client, request.ClientCallback)
	scope, scopeErr := normalizeScopes(request.Scope, client.Scopes)
	if err != nil || !boundedOpaqueValue(request.ClientState) || !boundedOpaqueValue(request.Nonce) ||
		!validPKCEValue(request.PKCEChallenge) || scopeErr != nil {
		return Client{}, nil, "", ErrInvalidRequest
	}
	return client, callback, scope, nil
}

func (service *Service) newAttempt(
	request BeginRequest,
	callback *url.URL,
	scope, state, upstreamVerifier string,
) storage.AuthAttempt {
	now := service.now().UTC()
	hash := sha256.Sum256([]byte(state))
	return storage.AuthAttempt{
		ID: uuid.NewString(), ProviderID: request.ProviderID, StateHash: hash[:],
		ClientState: request.ClientState, ClientCallback: callback.String(),
		ClientID: request.ClientID, Scope: scope,
		Nonce: request.Nonce, PKCEChallenge: request.PKCEChallenge,
		UpstreamPKCEVerifier: upstreamVerifier,
		CreatedAt:            now, ExpiresAt: now.Add(service.attemptTTL),
	}
}

func (service *Service) CompleteCallback(ctx context.Context, request CallbackRequest) (CallbackResult, error) {
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.UpstreamCode = strings.TrimSpace(request.UpstreamCode)
	request.UpstreamState = strings.TrimSpace(request.UpstreamState)
	if request.ProviderID == "" || request.UpstreamCode == "" || !boundedOpaqueValue(request.UpstreamState) {
		return CallbackResult{}, ErrInvalidRequest
	}
	hash := sha256.Sum256([]byte(request.UpstreamState))
	attempt, err := service.store.AuthTransactions().ConsumeAttempt(ctx, hash[:], service.now().UTC())
	if errors.Is(err, storage.ErrNotFound) {
		return CallbackResult{}, ErrExpiredOrReplayed
	}
	if err != nil {
		return CallbackResult{}, errors.New("consume login transaction")
	}
	if subtle.ConstantTimeCompare([]byte(attempt.ProviderID), []byte(request.ProviderID)) != 1 {
		return CallbackResult{}, ErrExpiredOrReplayed
	}
	provider, ok := service.providers.Provider(attempt.ProviderID)
	if !ok {
		return CallbackResult{}, ErrUnknownProvider
	}
	authorizationCodeProvider, ok := provider.(authn.AuthorizationCodeProvider)
	if !ok {
		return CallbackResult{}, ErrInvalidRequest
	}
	identity, err := authorizationCodeProvider.Exchange(
		ctx, request.UpstreamCode, attempt.UpstreamPKCEVerifier, attempt.Nonce,
	)
	if err != nil {
		return CallbackResult{}, errors.New("complete upstream login")
	}
	externalID, err := identity.ExternalID()
	if err != nil || identity.ProviderID != attempt.ProviderID {
		return CallbackResult{}, errors.New("upstream returned an invalid identity")
	}
	now := service.now().UTC()
	principal, err := service.store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: identity.ProviderID, ExternalID: externalID,
		DisplayName: identity.DisplayName, Email: identity.Email, Groups: identity.Groups,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return CallbackResult{}, errors.New("persist authenticated identity")
	}
	return service.completeAttempt(ctx, attempt, principal)
}

func (service *Service) CompleteLocal(ctx context.Context, transaction, principalID string) (CallbackResult, error) {
	hash := sha256.Sum256([]byte(strings.TrimSpace(transaction)))
	attempt, err := service.store.AuthTransactions().ConsumeAttempt(ctx, hash[:], service.now().UTC())
	if errors.Is(err, storage.ErrNotFound) {
		return CallbackResult{}, ErrExpiredOrReplayed
	}
	if err != nil || attempt.ProviderID != "local" {
		return CallbackResult{}, ErrInvalidRequest
	}
	principal, err := service.store.Principals().GetByID(ctx, strings.TrimSpace(principalID))
	if err != nil || principal.Provider != "local" {
		return CallbackResult{}, ErrInvalidRequest
	}
	return service.completeAttempt(ctx, attempt, principal)
}

func (service *Service) completeAttempt(
	ctx context.Context,
	attempt storage.AuthAttempt,
	principal storage.Principal,
) (CallbackResult, error) {
	exchangeCode, err := service.randomValue()
	if err != nil {
		return CallbackResult{}, errors.New("generate exchange code")
	}
	exchangeHash := sha256.Sum256([]byte(exchangeCode))
	now := service.now().UTC()
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		return repositories.AuthTransactions().CreateExchange(ctx, storage.AuthExchange{
			CodeHash: exchangeHash[:], PrincipalID: principal.ID, ProviderID: principal.Provider,
			ClientID: attempt.ClientID, RedirectURI: attempt.ClientCallback,
			Scope: attempt.Scope, Nonce: attempt.Nonce,
			PKCEChallenge: attempt.PKCEChallenge, CreatedAt: now, ExpiresAt: now.Add(service.exchangeTTL),
		})
	})
	if err != nil {
		return CallbackResult{}, errors.New("persist authenticated identity")
	}
	redirect, err := url.Parse(attempt.ClientCallback)
	if err != nil {
		return CallbackResult{}, errors.New("stored client callback is invalid")
	}
	query := redirect.Query()
	query.Set("code", exchangeCode)
	query.Set("state", attempt.ClientState)
	redirect.RawQuery = query.Encode()
	return CallbackResult{RedirectURL: redirect.String()}, nil
}

func (service *Service) Exchange(
	ctx context.Context,
	code, pkceVerifier, clientID, redirectURI string,
) (ExchangeResult, error) {
	code = strings.TrimSpace(code)
	pkceVerifier = strings.TrimSpace(pkceVerifier)
	clientID = strings.TrimSpace(clientID)
	redirectURI = strings.TrimSpace(redirectURI)
	if !boundedOpaqueValue(code) || !validPKCEValue(pkceVerifier) || clientID == "" || redirectURI == "" {
		return ExchangeResult{}, ErrInvalidRequest
	}
	hash := sha256.Sum256([]byte(code))
	exchange, err := service.store.AuthTransactions().ConsumeExchange(ctx, hash[:], service.now().UTC())
	if errors.Is(err, storage.ErrNotFound) {
		return ExchangeResult{}, ErrExpiredOrReplayed
	}
	if err != nil {
		return ExchangeResult{}, errors.New("consume exchange code")
	}
	verifierHash := sha256.Sum256([]byte(pkceVerifier))
	challenge := base64.RawURLEncoding.EncodeToString(verifierHash[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(exchange.PKCEChallenge)) != 1 {
		return ExchangeResult{}, ErrPKCEVerification
	}
	if subtle.ConstantTimeCompare([]byte(clientID), []byte(exchange.ClientID)) != 1 ||
		subtle.ConstantTimeCompare([]byte(redirectURI), []byte(exchange.RedirectURI)) != 1 {
		return ExchangeResult{}, ErrInvalidRequest
	}
	principal, err := service.store.Principals().GetByID(ctx, exchange.PrincipalID)
	if err != nil {
		return ExchangeResult{}, errors.New("load authenticated principal")
	}
	return ExchangeResult{Principal: principal, ClientID: exchange.ClientID, Scope: exchange.Scope, Nonce: exchange.Nonce}, nil
}

func (service *Service) randomValue() (string, error) {
	buffer := make([]byte, 32)
	if err := service.random(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validateLoopbackCallback(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidRequest
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	if address == nil || !address.IsLoopback() || (host != "127.0.0.1" && host != "::1") {
		return nil, ErrInvalidRequest
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") {
		return nil, ErrInvalidRequest
	}
	return parsed, nil
}

func validateConfiguredCallback(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	loopback := false
	if parsed != nil && parsed.Scheme == "http" {
		host := parsed.Hostname()
		address := net.ParseIP(host)
		loopback = strings.EqualFold(host, "localhost") || address != nil && address.IsLoopback()
	}
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !loopback) ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") {
		return "", errors.New("configured login callback must use HTTPS or loopback HTTP without query or fragment")
	}
	return parsed.String(), nil
}

func validateClientCallback(client Client, value string) (*url.URL, error) {
	if client.AllowLoopback {
		if callback, err := validateLoopbackCallback(value); err == nil {
			return callback, nil
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	for _, allowed := range client.RedirectURIs {
		if subtle.ConstantTimeCompare([]byte(parsed.String()), []byte(allowed)) == 1 {
			return parsed, nil
		}
	}
	return nil, ErrInvalidRequest
}

func normalizeScopes(value string, allowed []string) (string, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", errors.New("OAuth scopes are required")
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, scope := range allowed {
		allowedSet[strings.TrimSpace(scope)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(fields))
	for _, scope := range fields {
		if scope == "" || strings.ContainsAny(scope, "\"\\") {
			return "", errors.New("OAuth scope is invalid")
		}
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[scope]; !ok {
				return "", errors.New("OAuth scope is not allowed")
			}
		}
		if _, duplicate := seen[scope]; duplicate {
			return "", errors.New("OAuth scope is duplicated")
		}
		seen[scope] = struct{}{}
	}
	if _, ok := seen["openid"]; !ok {
		return "", errors.New("openid scope is required")
	}
	return strings.Join(fields, " "), nil
}

func boundedOpaqueValue(value string) bool {
	length := len(value)
	return length >= 32 && length <= 512
}

func validPKCEValue(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._~", character) {
			continue
		}
		return false
	}
	return true
}
