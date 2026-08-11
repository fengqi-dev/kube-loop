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

	CodeRateLimited        = "RATE_LIMITED"
	CodeInvalidCredentials = "INVALID_CREDENTIALS"
	CodeTokenUnavailable   = "TOKEN_UNAVAILABLE"
	CodeInvalidLogin       = "INVALID_LOGIN_REQUEST"
	CodeInvalidExchange    = "INVALID_EXCHANGE_CODE"
	CodeInvalidRefresh     = "INVALID_REFRESH_TOKEN"
	CodeInvalidArgument    = "INVALID_ARGUMENT"
)

type BrowserOpener func(string) error

type Config struct {
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	LoginTimeout   time.Duration
	OpenBrowser    BrowserOpener
}

type Client struct {
	httpClient     *http.Client
	requestTimeout time.Duration
	loginTimeout   time.Duration
	openBrowser    BrowserOpener
}

type tokenResponse struct {
	TokenType        string    `json:"tokenType"`
	AccessToken      string    `json:"accessToken"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshToken     string    `json:"refreshToken"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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
	return &Client{httpClient: &clone, requestTimeout: requestTimeout, loginTimeout: loginTimeout, openBrowser: config.OpenBrowser}
}

func (client *Client) LoginAD(
	ctx context.Context,
	baseURL, providerID, username string,
	password []byte,
	deviceID string,
) (credentials.Credential, error) {
	defer clear(password)
	baseURL, err := validateTarget(baseURL, providerID)
	if err != nil {
		return credentials.Credential{}, err
	}
	body := struct {
		Username string `json:"username"`
		Password string `json:"password"`
		DeviceID string `json:"deviceId"`
	}{Username: strings.TrimSpace(username), Password: string(password), DeviceID: strings.TrimSpace(deviceID)}
	if body.Username == "" || body.Password == "" || body.DeviceID == "" {
		body.Password = ""
		return credentials.Credential{}, errors.New("username, password and device ID are required")
	}
	var response tokenResponse
	err = client.postJSON(ctx, baseURL+"/auth/ad/"+providerID+"/login", body, &response)
	body.Password = ""
	if err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, deviceID)
}

func (client *Client) LoginStaticToken(
	ctx context.Context,
	baseURL, providerID string,
	staticToken []byte,
	deviceID string,
) (credentials.Credential, error) {
	defer clear(staticToken)
	baseURL, err := validateTarget(baseURL, providerID)
	if err != nil {
		return credentials.Credential{}, err
	}
	body := struct {
		Token    string `json:"token"`
		DeviceID string `json:"deviceId"`
	}{Token: string(staticToken), DeviceID: strings.TrimSpace(deviceID)}
	if body.Token == "" || body.DeviceID == "" {
		body.Token = ""
		return credentials.Credential{}, errors.New("static token and device ID are required")
	}
	var response tokenResponse
	err = client.postJSON(ctx, baseURL+"/auth/static-token/"+providerID+"/login", body, &response)
	body.Token = ""
	if err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, deviceID)
}

func (client *Client) LoginAnonymous(
	ctx context.Context,
	baseURL, providerID, deviceID string,
) (credentials.Credential, error) {
	baseURL, err := validateTarget(baseURL, providerID)
	if err != nil {
		return credentials.Credential{}, err
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return credentials.Credential{}, errors.New("device ID is required")
	}
	var response tokenResponse
	if err := client.postJSON(ctx, baseURL+"/auth/anonymous/"+providerID+"/login", struct {
		DeviceID string `json:"deviceId"`
	}{DeviceID: deviceID}, &response); err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, deviceID)
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
	startBody := struct {
		ClientCallback string `json:"clientCallback"`
		State          string `json:"state"`
		Nonce          string `json:"nonce"`
		PKCEChallenge  string `json:"pkceChallenge"`
	}{ClientCallback: callbackURL, State: state, Nonce: nonce, PKCEChallenge: challenge}
	var startResponse struct {
		AuthorizationURL string    `json:"authorizationUrl"`
		ExpiresAt        time.Time `json:"expiresAt"`
	}
	if err := client.postJSON(ctx, baseURL+"/auth/oidc/"+providerID+"/start", startBody, &startResponse); err != nil {
		return credentials.Credential{}, err
	}
	if startResponse.ExpiresAt.IsZero() || !startResponse.ExpiresAt.After(time.Now()) {
		return credentials.Credential{}, errors.New("Gateway returned an expired login transaction")
	}
	authorizationURL, err := url.Parse(startResponse.AuthorizationURL)
	if err != nil || !authorizationURL.IsAbs() || authorizationURL.Scheme != "https" || authorizationURL.Host == "" {
		return credentials.Credential{}, errors.New("Gateway returned an invalid authorization URL")
	}
	loginDeadline := time.Now().Add(client.loginTimeout)
	if startResponse.ExpiresAt.Before(loginDeadline) {
		loginDeadline = startResponse.ExpiresAt
	}
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
	exchangeBody := struct {
		Code         string `json:"code"`
		PKCEVerifier string `json:"pkceVerifier"`
		DeviceID     string `json:"deviceId"`
	}{Code: result.code, PKCEVerifier: verifier, DeviceID: deviceID}
	var response tokenResponse
	if err := client.postJSON(ctx, baseURL+"/auth/token/exchange", exchangeBody, &response); err != nil {
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
	if err := client.postJSON(ctx, baseURL+"/auth/token/refresh", struct {
		RefreshToken string `json:"refreshToken"`
	}{RefreshToken: current.RefreshToken}, &response); err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, current.DeviceID)
}

func (client *Client) Revoke(ctx context.Context, baseURL, refreshToken string) error {
	baseURL, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	return client.postJSON(ctx, baseURL+"/auth/token/revoke", struct {
		RefreshToken string `json:"refreshToken"`
	}{RefreshToken: refreshToken}, nil)
}

func (client *Client) postJSON(ctx context.Context, endpoint string, body, destination any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return errors.New("encode authentication request")
	}
	defer clear(raw)
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("create authentication request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
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
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("KubeLoop login complete. You can close this window.\n"))
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
		response.AccessExpiresAt.IsZero() || response.RefreshExpiresAt.IsZero() {
		return credentials.Credential{}, errors.New("Gateway returned an incomplete token response")
	}
	return credentials.Credential{
		TokenType: "Bearer", AccessToken: response.AccessToken, AccessExpiresAt: response.AccessExpiresAt,
		RefreshToken: response.RefreshToken, RefreshExpiresAt: response.RefreshExpiresAt, DeviceID: deviceID,
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
