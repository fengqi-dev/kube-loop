package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/authconfig"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

const (
	DefaultRequestTimeout = 15 * time.Second
	DefaultLoginTimeout   = 5 * time.Minute
	maxResponseBytes      = 64 << 10

	CodeInvalidRequest         = "invalid_request"
	CodeInvalidClient          = "invalid_client"
	CodeInvalidGrant           = "invalid_grant"
	CodeUnsupportedGrantType   = "unsupported_grant_type"
	CodeInvalidToken           = "invalid_token"
	CodeTemporarilyUnavailable = "temporarily_unavailable"
)

var ErrLoginExpired = errors.New("gateway login expired; sign in again")

type BrowserOpener func(string) error

type Config struct {
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	LoginTimeout     time.Duration
	OpenBrowser      BrowserOpener
	BrowserCallback  func()
	ClientID         string
	RedirectURI      string
	LoopbackCallback bool
}

type Client struct {
	httpClient       *http.Client
	requestTimeout   time.Duration
	loginTimeout     time.Duration
	openBrowser      BrowserOpener
	browserCallback  func()
	clientID         string
	redirectURI      string
	loopbackCallback bool
	callbackMu       sync.Mutex
	pendingCallback  *pendingCallback
}

type tokenResponse struct {
	TokenType    string `json:"token_type"`
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

type errorResponse struct {
	Code    string `json:"error"`
	Message string `json:"error_description"`
}

type providerMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

// APIError is a typed authentication endpoint rejection. Callers can branch
// on Status or Code without parsing server-controlled human-readable text.
type APIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (apiError *APIError) Error() string {
	if apiError == nil {
		return ""
	}
	if apiError.Code != "" {
		return fmt.Sprintf("authentication failed (%s): %s", apiError.Code, apiError.Message)
	}
	return fmt.Sprintf("authentication request returned HTTP %d", apiError.Status)
}

// IsInvalidGrant reports an unrecoverable OAuth grant rejection. Callers must
// discard the local credential and require a new browser login rather than
// retrying a refresh token that the server has expired or revoked.
func IsInvalidGrant(err error) bool {
	var apiError *APIError
	return errors.As(err, &apiError) && apiError.Code == CodeInvalidGrant
}

type callbackResult struct {
	code string
	err  error
}

type pendingCallback struct {
	state     string
	result    chan callbackResult
	delivered bool
}

func New(config Config) *Client {
	requestTimeout := config.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}
	loginTimeout := config.LoginTimeout
	if loginTimeout <= 0 {
		loginTimeout = DefaultLoginTimeout
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clone := *httpClient
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	clientID := strings.TrimSpace(config.ClientID)
	if clientID == "" {
		clientID = DefaultClientID
	}
	redirectURI := strings.TrimSpace(config.RedirectURI)
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}
	return &Client{
		httpClient: &clone, requestTimeout: requestTimeout, loginTimeout: loginTimeout,
		openBrowser: config.OpenBrowser, browserCallback: config.BrowserCallback,
		clientID: clientID, redirectURI: redirectURI, loopbackCallback: config.LoopbackCallback,
	}
}

func (client *Client) LoginOIDC(
	ctx context.Context,
	baseURL, providerID, deviceID string,
) (credentials.Credential, error) {
	baseURL, err := validateTarget(baseURL, providerID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if strings.TrimSpace(deviceID) == "" {
		return credentials.Credential{}, errors.New("device ID is required")
	}
	if client.openBrowser == nil {
		return credentials.Credential{}, errors.New("browser integration is unavailable")
	}
	state, err := randomValue(32)
	if err != nil {
		return credentials.Credential{}, err
	}
	nonce, err := randomValue(32)
	if err != nil {
		return credentials.Credential{}, err
	}
	verifier, err := randomValue(32)
	if err != nil {
		return credentials.Credential{}, err
	}
	verifierHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(verifierHash[:])
	metadata, err := client.discoverProvider(ctx, baseURL)
	if err != nil {
		return credentials.Credential{}, err
	}
	authorizationURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return credentials.Credential{}, errors.New("create authorization URL")
	}
	loginDeadline := time.Now().Add(client.loginTimeout)
	loginContext, cancel := context.WithDeadline(ctx, loginDeadline)
	defer cancel()
	callback, err := client.beginCallback(state)
	if err != nil {
		return credentials.Credential{}, err
	}
	defer client.endCallback(callback)
	redirectURI := client.redirectURI
	if client.loopbackCallback {
		actualRedirectURI, closeCallback, callbackErr := client.startLoopbackCallback(loginContext)
		if callbackErr != nil {
			return credentials.Credential{}, callbackErr
		}
		redirectURI = actualRedirectURI
		defer closeCallback()
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set(authParamClientID, client.clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", "openid profile email offline_access kubeloop.api")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("provider", providerID)
	authorizationURL.RawQuery = query.Encode()
	if err := client.openBrowser(authorizationURL.String()); err != nil {
		return credentials.Credential{}, errors.New("open system browser")
	}
	var result callbackResult
	select {
	case result = <-callback.result:
		if client.browserCallback != nil {
			client.browserCallback()
		}
	case <-loginContext.Done():
		if errors.Is(loginContext.Err(), context.DeadlineExceeded) {
			return credentials.Credential{}, errors.New("browser login timed out")
		}
		return credentials.Credential{}, errors.New("browser login was cancelled")
	}
	if result.err != nil {
		return credentials.Credential{}, result.err
	}
	var response tokenResponse
	if err := client.postForm(loginContext, metadata.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {result.code}, "code_verifier": {verifier},
		authParamClientID: {client.clientID}, "redirect_uri": {redirectURI}, "device_id": {deviceID},
	}, &response); err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, deviceID)
}

func (client *Client) Refresh(
	ctx context.Context,
	baseURL string,
	current credentials.Credential,
) (credentials.Credential, error) {
	baseURL, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return credentials.Credential{}, err
	}
	var response tokenResponse
	metadata, err := client.discoverProvider(ctx, baseURL)
	if err != nil {
		return credentials.Credential{}, err
	}
	if err := client.postForm(ctx, metadata.TokenEndpoint, url.Values{
		"grant_type": {authParamRefreshToken}, authParamRefreshToken: {current.RefreshToken},
		authParamClientID: {client.clientID}, "device_id": {current.DeviceID},
	}, &response); err != nil {
		return credentials.Credential{}, err
	}
	next, err := credentialFromResponse(response, current.DeviceID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if next.IdentityID == "" {
		next.IdentityID = current.IdentityID
	}
	if next.UserName == "" {
		next.UserName = current.UserName
	}
	return next, nil
}

func (client *Client) Revoke(ctx context.Context, baseURL, refreshToken string) error {
	baseURL, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	metadata, err := client.discoverProvider(ctx, baseURL)
	if err != nil {
		return err
	}
	return client.postForm(ctx, metadata.RevocationEndpoint, url.Values{
		"token": {refreshToken}, "token_type_hint": {authParamRefreshToken}, authParamClientID: {client.clientID},
	}, nil)
}

const (
	DefaultClientID    = authconfig.DesktopClientID
	DefaultRedirectURI = authconfig.DesktopRedirectURI
)

func (client *Client) discoverProvider(ctx context.Context, baseURL string) (providerMetadata, error) {
	var metadata providerMetadata
	if err := client.getJSON(ctx, baseURL+"/.well-known/openid-configuration", &metadata); err != nil {
		return providerMetadata{}, err
	}
	if metadata.Issuer != baseURL || !sameOriginEndpoint(baseURL, metadata.AuthorizationEndpoint) ||
		!sameOriginEndpoint(
			baseURL,
			metadata.TokenEndpoint,
		) || !sameOriginEndpoint(baseURL, metadata.RevocationEndpoint) {
		return providerMetadata{}, errors.New("oIDC discovery returned invalid provider metadata")
	}
	return metadata, nil
}

func sameOriginEndpoint(baseURL, endpoint string) bool {
	base, baseErr := url.Parse(baseURL)
	target, targetErr := url.Parse(endpoint)
	return baseErr == nil && targetErr == nil && target.IsAbs() && target.User == nil && target.RawQuery == "" &&
		target.Fragment == "" && strings.EqualFold(base.Scheme, target.Scheme) && strings.EqualFold(base.Host, target.Host)
}

func (client *Client) getJSON(ctx context.Context, endpoint string, destination any) error {
	return client.request(ctx, http.MethodGet, endpoint, "", nil, destination)
}

func (client *Client) postForm(ctx context.Context, endpoint string, form url.Values, destination any) error {
	return client.request(
		ctx,
		http.MethodPost,
		endpoint,
		"application/x-www-form-urlencoded",
		[]byte(form.Encode()),
		destination,
	)
}

func (client *Client) request(
	ctx context.Context,
	method,
	endpoint,
	contentType string,
	raw []byte,
	destination any,
) (resultErr error) {
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("create authentication request")
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return errors.New("authentication request timed out")
		}
		return fmt.Errorf("authentication request failed: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close authentication response: %w", err))
		}
	}()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("read authentication response")
	}
	if len(responseRaw) > maxResponseBytes {
		return errors.New("authentication response exceeds 64 KiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response.StatusCode, response.Header.Get("X-Request-ID"), responseRaw)
	}
	if destination == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseRaw))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("authentication response contains invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("authentication response must contain one JSON document")
	}
	return nil
}

func decodeAPIError(status int, requestID string, raw []byte) error {
	var document errorResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err == nil {
		if trailing := decoder.Decode(&struct{}{}); errors.Is(trailing, io.EOF) && document.Code != "" {
			return &APIError{
				Status: status, Code: document.Code, Message: document.Message,
				RequestID: strings.TrimSpace(requestID),
			}
		}
	}
	return &APIError{Status: status, RequestID: strings.TrimSpace(requestID)}
}

func (client *Client) beginCallback(state string) (*pendingCallback, error) {
	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	if client.pendingCallback != nil {
		return nil, errors.New("browser login is already in progress")
	}
	pending := &pendingCallback{state: state, result: make(chan callbackResult, 1)}
	client.pendingCallback = pending
	return pending, nil
}

func (client *Client) endCallback(pending *pendingCallback) {
	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	if client.pendingCallback == pending {
		client.pendingCallback = nil
	}
}

func (client *Client) startLoopbackCallback(ctx context.Context) (string, func(), error) {
	redirect, err := url.Parse(client.redirectURI)
	if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" ||
		redirect.Port() != "" || redirect.Path != "/callback" || redirect.User != nil ||
		redirect.RawQuery != "" || redirect.Fragment != "" {
		return "", nil, errors.New("loopback login redirect URI is invalid")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(redirect.Hostname(), "0"))
	if err != nil {
		return "", nil, fmt.Errorf("listen for browser login callback: %w", err)
	}
	actualRedirect := *redirect
	actualRedirect.Host = listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, func(rw http.ResponseWriter, request *http.Request) {
		rw.Header().Set("Cache-Control", "no-store")
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		if request.Method != http.MethodGet || request.Host != actualRedirect.Host {
			http.Error(rw, "Invalid login callback.", http.StatusBadRequest)
			return
		}
		callbackURL := actualRedirect
		callbackURL.RawQuery = request.URL.RawQuery
		if err := client.HandleCallbackURL(callbackURL.String()); err != nil {
			http.Error(rw, "Login callback was rejected. Return to the terminal and try again.", http.StatusBadRequest)
			return
		}
		const loginCompleteHTML = "<!doctype html><title>KubeLoop login complete</title>" +
			"<style>body{font-family:sans-serif;max-width:40rem;margin:15vh auto;padding:2rem}" +
			"h1{color:#087f5b}</style><h1>Login complete</h1>" +
			"<p>You can close this window and return to KubeLoop TUI.</p>"
		_, _ = io.WriteString(rw, loginCompleteHTML)
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       5 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	return actualRedirect.String(), func() {
		_ = server.Close()
	}, nil
}

// HandleCallbackURL completes the active browser login from the desktop URL
// protocol handler or the TUI loopback listener. Invalid or stale URLs never
// consume the pending login.
func (client *Client) HandleCallbackURL(rawURL string) error {
	callbackURL, err := url.Parse(rawURL)
	redirectURL, redirectErr := url.Parse(client.redirectURI)
	if err != nil || redirectErr != nil || !client.matchesCallbackTarget(callbackURL, redirectURL) {
		return errors.New("login callback URL is invalid")
	}
	query := callbackURL.Query()
	states := query["state"]
	if len(states) != 1 {
		return errors.New("login callback state is invalid")
	}

	client.callbackMu.Lock()
	defer client.callbackMu.Unlock()
	pending := client.pendingCallback
	if pending == nil {
		return errors.New("no browser login is in progress")
	}
	state := states[0]
	if len(state) != len(pending.state) || subtle.ConstantTimeCompare([]byte(state), []byte(pending.state)) != 1 {
		return errors.New("login callback state is invalid")
	}
	if pending.delivered {
		return errors.New("login callback was already consumed")
	}

	var result callbackResult
	callbackErrors := query["error"]
	if len(callbackErrors) > 1 {
		return errors.New("login callback error is invalid")
	}
	if len(callbackErrors) == 1 && callbackErrors[0] != "" {
		result.err = errors.New("identity provider rejected the login request")
	} else {
		codes := query["code"]
		if len(codes) != 1 || len(codes[0]) < 32 || len(codes[0]) > 512 {
			return errors.New("login callback code is invalid")
		}
		result.code = codes[0]
	}
	pending.result <- result
	pending.delivered = true
	return nil
}

func (client *Client) matchesCallbackTarget(callbackURL, redirectURL *url.URL) bool {
	if callbackURL == nil || redirectURL == nil || callbackURL.User != nil || callbackURL.Fragment != "" ||
		callbackURL.Scheme != redirectURL.Scheme || callbackURL.Path != redirectURL.Path {
		return false
	}
	if callbackURL.Host == redirectURL.Host {
		return true
	}
	validLoopback := client.loopbackCallback && callbackURL.Scheme == "http" && callbackURL.Port() != ""
	sameHost := callbackURL.Hostname() == "127.0.0.1" && redirectURL.Hostname() == callbackURL.Hostname()
	return validLoopback && sameHost && redirectURL.Port() == ""
}

func credentialFromResponse(response tokenResponse, deviceID string) (credentials.Credential, error) {
	missingToken := response.AccessToken == "" || response.RefreshToken == ""
	if !strings.EqualFold(response.TokenType, authorizationTypeBearer) || missingToken || response.ExpiresIn <= 0 {
		return credentials.Credential{}, errors.New("oAuth server returned an incomplete token response")
	}
	identityID, userName := identityFromIDToken(response.IDToken)
	return credentials.Credential{
		TokenType:       authorizationTypeBearer,
		AccessToken:     response.AccessToken,
		AccessExpiresAt: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second),
		RefreshToken:    response.RefreshToken,
		DeviceID:        deviceID,
		IdentityID:      identityID,
		UserName:        userName,
	}, nil
}

func identityFromIDToken(token string) (string, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Subject           string `json:"sub"`
		Name              string `json:"name"`
		PreferredUserName string `json:"preferred_username"`
		UserName          string `json:"username"`
		Email             string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", ""
	}
	identityID := strings.TrimSpace(claims.Subject)
	for _, value := range []string{claims.Name, claims.PreferredUserName, claims.UserName, claims.Email, identityID} {
		if value = strings.TrimSpace(value); value != "" {
			return identityID, value
		}
	}
	return identityID, ""
}

func validateTarget(baseURL, providerID string) (string, error) {
	normalized, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	if !validProviderID(providerID) {
		return "", errors.New("authentication provider ID is invalid")
	}
	return normalized, nil
}

func validProviderID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}

func randomValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate login secret")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
