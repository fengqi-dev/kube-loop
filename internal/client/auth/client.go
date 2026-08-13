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
	"time"

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

type BrowserOpener func(string) error

type Config struct {
	HTTPClient      *http.Client
	RequestTimeout  time.Duration
	LoginTimeout    time.Duration
	OpenBrowser     BrowserOpener
	BrowserCallback func()
}

type Client struct {
	httpClient      *http.Client
	requestTimeout  time.Duration
	loginTimeout    time.Duration
	openBrowser     BrowserOpener
	browserCallback func()
}

type tokenResponse struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	IDToken          string `json:"id_token"`
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

type callbackResult struct {
	code string
	err  error
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
	return &Client{
		httpClient: &clone, requestTimeout: requestTimeout, loginTimeout: loginTimeout,
		openBrowser: config.OpenBrowser, browserCallback: config.BrowserCallback,
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
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return credentials.Credential{}, errors.New("start loopback login callback")
	}
	defer listener.Close()
	callbackURL := "http://" + listener.Addr().String() + "/callback"
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
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", DefaultClientID)
	query.Set("redirect_uri", callbackURL)
	query.Set("scope", "openid profile email offline_access kubeloop.api")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("provider", providerID)
	authorizationURL.RawQuery = query.Encode()
	loginDeadline := time.Now().Add(client.loginTimeout)
	loginContext, cancel := context.WithDeadline(ctx, loginDeadline)
	defer cancel()
	callback := make(chan callbackResult, 1)
	server := newLoopbackServer(state, callback)
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(serveDone)
	}()
	if err := client.openBrowser(authorizationURL.String()); err != nil {
		_ = server.Close()
		<-serveDone
		return credentials.Credential{}, errors.New("open system browser")
	}
	var result callbackResult
	select {
	case result = <-callback:
		if client.browserCallback != nil {
			client.browserCallback()
		}
	case <-loginContext.Done():
		_ = server.Close()
		<-serveDone
		if errors.Is(loginContext.Err(), context.DeadlineExceeded) {
			return credentials.Credential{}, errors.New("browser login timed out")
		}
		return credentials.Credential{}, errors.New("browser login was cancelled")
	}
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	_ = server.Shutdown(shutdownContext)
	shutdownCancel()
	<-serveDone
	if result.err != nil {
		return credentials.Credential{}, result.err
	}
	var response tokenResponse
	if err := client.postForm(ctx, metadata.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {result.code}, "code_verifier": {verifier},
		"client_id": {DefaultClientID}, "redirect_uri": {callbackURL}, "device_id": {deviceID},
	}, &response); err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, deviceID)
}

func (client *Client) Refresh(ctx context.Context, baseURL string, current credentials.Credential) (credentials.Credential, error) {
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
		"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken},
		"client_id": {DefaultClientID}, "device_id": {current.DeviceID},
	}, &response); err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, current.DeviceID)
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
		"token": {refreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {DefaultClientID},
	}, nil)
}

const DefaultClientID = "kubeloop-desktop"

func (client *Client) discoverProvider(ctx context.Context, baseURL string) (providerMetadata, error) {
	var metadata providerMetadata
	if err := client.getJSON(ctx, baseURL+"/.well-known/openid-configuration", &metadata); err != nil {
		return providerMetadata{}, err
	}
	if metadata.Issuer != baseURL || !sameOriginEndpoint(baseURL, metadata.AuthorizationEndpoint) ||
		!sameOriginEndpoint(baseURL, metadata.TokenEndpoint) || !sameOriginEndpoint(baseURL, metadata.RevocationEndpoint) {
		return providerMetadata{}, errors.New("OIDC discovery returned invalid provider metadata")
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
	return client.request(ctx, http.MethodPost, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode()), destination)
}

func (client *Client) request(ctx context.Context, method, endpoint, contentType string, raw []byte, destination any) error {
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
	defer response.Body.Close()
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

func newLoopbackServer(expectedState string, result chan<- callbackResult) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		state := request.URL.Query().Get("state")
		if len(state) != len(expectedState) || subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) != 1 {
			http.Error(writer, "invalid login state", http.StatusBadRequest)
			return
		}
		if request.URL.Query().Get("error") != "" {
			select {
			case result <- callbackResult{err: errors.New("identity provider rejected the login request")}:
			default:
			}
			http.Error(writer, "KubeLoop login failed. Return to the application.", http.StatusBadRequest)
			return
		}
		code := request.URL.Query().Get("code")
		if len(code) < 32 || len(code) > 512 {
			http.Error(writer, "invalid login code", http.StatusBadRequest)
			return
		}
		select {
		case result <- callbackResult{code: code}:
			closeScript := "window.close();"
			digest := sha256.Sum256([]byte(closeScript))
			writer.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+
				base64.StdEncoding.EncodeToString(digest[:])+"'; frame-ancestors 'none'; base-uri 'none'")
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><title>KubeLoop login complete</title></head><body><p>Login complete. Returning to KubeLoop…</p><script>" + closeScript + "</script></body></html>"))
		default:
			http.Error(writer, "login callback already consumed", http.StatusConflict)
		}
	})
	return &http.Server{
		Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10,
	}
}

func credentialFromResponse(response tokenResponse, deviceID string) (credentials.Credential, error) {
	if !strings.EqualFold(response.TokenType, "Bearer") || response.AccessToken == "" || response.RefreshToken == "" ||
		response.ExpiresIn <= 0 || response.RefreshExpiresIn <= 0 {
		return credentials.Credential{}, errors.New("Gateway returned an incomplete token response")
	}
	return credentials.Credential{
		TokenType: "Bearer", AccessToken: response.AccessToken, AccessExpiresAt: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second),
		RefreshToken: response.RefreshToken, RefreshExpiresAt: time.Now().Add(time.Duration(response.RefreshExpiresIn) * time.Second), DeviceID: deviceID,
	}, nil
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
