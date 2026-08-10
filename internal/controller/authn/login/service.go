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

	"github.com/fengqi-dev/kube-loop/internal/controller/authn"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
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
	AttemptTTL       time.Duration
	ExchangeTTL      time.Duration
	AllowedCallbacks []string
	Now              func() time.Time
	Random           func([]byte) error
}

type Service struct {
	providers   *authn.Registry
	store       Store
	attemptTTL  time.Duration
	exchangeTTL time.Duration
	callbacks   map[string]struct{}
	now         func() time.Time
	random      func([]byte) error
}

type BeginRequest struct {
	ProviderID     string
	ClientCallback string
	ClientState    string
	Nonce          string
	PKCEChallenge  string
}

type BeginResult struct {
	AuthorizationURL string
	ExpiresAt        time.Time
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
}

func (service *Service) AuthenticatePassword(
	ctx context.Context,
	providerID string,
	credentials authn.PasswordCredentials,
) (ExchangeResult, error) {
	providerID = strings.TrimSpace(providerID)
	provider, ok := service.providers.Provider(providerID)
	if !ok {
		zeroBytes(credentials.Password)
		return ExchangeResult{}, ErrUnknownProvider
	}
	passwordProvider, ok := provider.(authn.PasswordProvider)
	if !ok {
		zeroBytes(credentials.Password)
		return ExchangeResult{}, ErrInvalidRequest
	}
	identity, err := passwordProvider.AuthenticatePassword(ctx, credentials)
	if err != nil {
		return ExchangeResult{}, err
	}
	return service.persistDirectIdentity(ctx, providerID, identity)
}

func (service *Service) AuthenticateToken(
	ctx context.Context,
	providerID string,
	credentials authn.TokenCredentials,
) (ExchangeResult, error) {
	providerID = strings.TrimSpace(providerID)
	provider, ok := service.providers.Provider(providerID)
	if !ok {
		zeroBytes(credentials.Token)
		return ExchangeResult{}, ErrUnknownProvider
	}
	tokenProvider, ok := provider.(authn.TokenProvider)
	if !ok {
		zeroBytes(credentials.Token)
		return ExchangeResult{}, ErrInvalidRequest
	}
	identity, err := tokenProvider.AuthenticateToken(ctx, credentials)
	if err != nil {
		return ExchangeResult{}, err
	}
	return service.persistDirectIdentity(ctx, providerID, identity)
}

func (service *Service) AuthenticateAnonymous(ctx context.Context, providerID string) (ExchangeResult, error) {
	providerID = strings.TrimSpace(providerID)
	provider, ok := service.providers.Provider(providerID)
	if !ok {
		return ExchangeResult{}, ErrUnknownProvider
	}
	anonymousProvider, ok := provider.(authn.AnonymousProvider)
	if !ok {
		return ExchangeResult{}, ErrInvalidRequest
	}
	identity, err := anonymousProvider.AuthenticateAnonymous(ctx)
	if err != nil {
		return ExchangeResult{}, err
	}
	return service.persistDirectIdentity(ctx, providerID, identity)
}

func (service *Service) persistDirectIdentity(
	ctx context.Context,
	providerID string,
	identity authn.Identity,
) (ExchangeResult, error) {
	externalID, err := identity.ExternalID()
	if err != nil || identity.ProviderID != providerID {
		return ExchangeResult{}, errors.New("authentication provider returned an invalid identity")
	}
	now := service.now().UTC()
	principal, err := service.store.Principals().Upsert(ctx, storage.Principal{
		ID: uuid.NewString(), Provider: identity.ProviderID, ExternalID: externalID,
		DisplayName: identity.DisplayName, Email: identity.Email, Groups: identity.Groups,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return ExchangeResult{}, errors.New("persist authenticated identity")
	}
	return ExchangeResult{Principal: principal}, nil
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
	callbacks := make(map[string]struct{}, len(config.AllowedCallbacks))
	for _, value := range config.AllowedCallbacks {
		callback, err := validateConfiguredCallback(value)
		if err != nil {
			return nil, err
		}
		if _, exists := callbacks[callback]; exists {
			return nil, errors.New("login callback allowlist contains a duplicate")
		}
		callbacks[callback] = struct{}{}
	}
	return &Service{
		providers: providers, store: store,
		attemptTTL: config.AttemptTTL, exchangeTTL: config.ExchangeTTL,
		callbacks: callbacks, now: config.Now, random: config.Random,
	}, nil
}

func (service *Service) Begin(ctx context.Context, request BeginRequest) (BeginResult, error) {
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.ClientState = strings.TrimSpace(request.ClientState)
	request.Nonce = strings.TrimSpace(request.Nonce)
	request.PKCEChallenge = strings.TrimSpace(request.PKCEChallenge)
	callback, err := service.validateClientCallback(request.ClientCallback)
	if err != nil || !boundedOpaqueValue(request.ClientState) || !boundedOpaqueValue(request.Nonce) ||
		!validPKCEValue(request.PKCEChallenge) {
		return BeginResult{}, ErrInvalidRequest
	}
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
	now := service.now().UTC()
	hash := sha256.Sum256([]byte(upstreamState))
	attempt := storage.AuthAttempt{
		ID: uuid.NewString(), ProviderID: request.ProviderID, StateHash: hash[:],
		ClientState: request.ClientState, ClientCallback: callback.String(),
		Nonce: request.Nonce, PKCEChallenge: request.PKCEChallenge,
		UpstreamPKCEVerifier: upstreamVerifier,
		CreatedAt:            now, ExpiresAt: now.Add(service.attemptTTL),
	}
	if err := service.store.AuthTransactions().CreateAttempt(ctx, attempt); err != nil {
		return BeginResult{}, errors.New("persist login transaction")
	}
	return BeginResult{AuthorizationURL: authorizationURL, ExpiresAt: attempt.ExpiresAt}, nil
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
	exchangeCode, err := service.randomValue()
	if err != nil {
		return CallbackResult{}, errors.New("generate exchange code")
	}
	exchangeHash := sha256.Sum256([]byte(exchangeCode))
	now := service.now().UTC()
	var principal storage.Principal
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		var err error
		principal, err = repositories.Principals().Upsert(ctx, storage.Principal{
			ID: uuid.NewString(), Provider: identity.ProviderID, ExternalID: externalID,
			DisplayName: identity.DisplayName, Email: identity.Email, Groups: identity.Groups,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		return repositories.AuthTransactions().CreateExchange(ctx, storage.AuthExchange{
			CodeHash: exchangeHash[:], PrincipalID: principal.ID, ProviderID: identity.ProviderID,
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

func (service *Service) Exchange(ctx context.Context, code, pkceVerifier string) (ExchangeResult, error) {
	code = strings.TrimSpace(code)
	pkceVerifier = strings.TrimSpace(pkceVerifier)
	if !boundedOpaqueValue(code) || !validPKCEValue(pkceVerifier) {
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
	principal, err := service.store.Principals().GetByID(ctx, exchange.PrincipalID)
	if err != nil {
		return ExchangeResult{}, errors.New("load authenticated principal")
	}
	return ExchangeResult{Principal: principal}, nil
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

func (service *Service) validateClientCallback(value string) (*url.URL, error) {
	if callback, err := validateLoopbackCallback(value); err == nil {
		return callback, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || service == nil {
		return nil, ErrInvalidRequest
	}
	if _, allowed := service.callbacks[parsed.String()]; !allowed {
		return nil, ErrInvalidRequest
	}
	return parsed, nil
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

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
