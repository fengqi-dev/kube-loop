package httpauth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controller/authn"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn/login"
	"github.com/fengqi-dev/kube-loop/internal/controller/authn/token"
)

const maxAuthBodyBytes = 16 << 10

type Handler struct {
	login     *login.Service
	tokens    *token.Service
	passwords *passwordLimiter
}

type startRequest struct {
	ClientCallback string `json:"clientCallback"`
	State          string `json:"state"`
	Nonce          string `json:"nonce"`
	PKCEChallenge  string `json:"pkceChallenge"`
}

type startResponse struct {
	AuthorizationURL string `json:"authorizationUrl"`
	ExpiresAt        string `json:"expiresAt"`
}

type exchangeRequest struct {
	Code         string `json:"code"`
	PKCEVerifier string `json:"pkceVerifier"`
	DeviceID     string `json:"deviceId"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type passwordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	DeviceID string `json:"deviceId"`
}

type staticTokenRequest struct {
	Token    string `json:"token"`
	DeviceID string `json:"deviceId"`
}

type anonymousRequest struct {
	DeviceID string `json:"deviceId"`
}

type tokenResponse struct {
	TokenType        string `json:"tokenType"`
	AccessToken      string `json:"accessToken"`
	AccessExpiresAt  string `json:"accessExpiresAt"`
	RefreshToken     string `json:"refreshToken"`
	RefreshExpiresAt string `json:"refreshExpiresAt"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(loginService *login.Service, tokenService *token.Service) (*Handler, error) {
	if loginService == nil || tokenService == nil {
		return nil, errors.New("login and token services are required")
	}
	return &Handler{login: loginService, tokens: tokenService, passwords: newPasswordLimiter()}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	path := strings.TrimPrefix(request.URL.Path, "/auth/")
	parts := strings.Split(path, "/")
	switch {
	case request.Method == http.MethodPost && len(parts) == 3 && parts[0] == "oidc" && parts[2] == "start":
		handler.start(writer, request, parts[1])
	case request.Method == http.MethodGet && len(parts) == 2 && parts[0] == "callback":
		handler.callback(writer, request, parts[1])
	case request.Method == http.MethodPost && len(parts) == 3 && parts[0] == "ad" && parts[2] == "login":
		handler.password(writer, request, parts[1])
	case request.Method == http.MethodPost && len(parts) == 3 && parts[0] == "static-token" && parts[2] == "login":
		handler.staticToken(writer, request, parts[1])
	case request.Method == http.MethodPost && len(parts) == 3 && parts[0] == "anonymous" && parts[2] == "login":
		handler.anonymous(writer, request, parts[1])
	case request.Method == http.MethodPost && path == "token/exchange":
		handler.exchange(writer, request)
	case request.Method == http.MethodPost && path == "token/refresh":
		handler.refresh(writer, request)
	case request.Method == http.MethodPost && path == "token/revoke":
		handler.revoke(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (handler *Handler) staticToken(writer http.ResponseWriter, request *http.Request, providerID string) {
	var body staticTokenRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if !handler.passwords.allow(providerID, "static-token", request.RemoteAddr) {
		body.Token = ""
		writeError(writer, http.StatusTooManyRequests, "RATE_LIMITED", "login attempt limit exceeded")
		return
	}
	presented := []byte(body.Token)
	body.Token = ""
	result, err := handler.login.AuthenticateToken(request.Context(), providerID, authn.TokenCredentials{Token: presented})
	clear(presented)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "INVALID_CREDENTIALS", "credentials were rejected")
		return
	}
	handler.passwords.success(providerID, "static-token")
	handler.issue(writer, request, result, body.DeviceID)
}

func (handler *Handler) anonymous(writer http.ResponseWriter, request *http.Request, providerID string) {
	var body anonymousRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if !handler.passwords.allow(providerID, "anonymous", request.RemoteAddr) {
		writeError(writer, http.StatusTooManyRequests, "RATE_LIMITED", "login attempt limit exceeded")
		return
	}
	result, err := handler.login.AuthenticateAnonymous(request.Context(), providerID)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "INVALID_CREDENTIALS", "credentials were rejected")
		return
	}
	handler.passwords.success(providerID, "anonymous")
	handler.issue(writer, request, result, body.DeviceID)
}

func (handler *Handler) issue(writer http.ResponseWriter, request *http.Request, result login.ExchangeResult, deviceID string) {
	pair, err := handler.tokens.Issue(request.Context(), result.Principal, deviceID)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "TOKEN_UNAVAILABLE", "token service is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, tokenPayload(pair))
}

func (handler *Handler) password(writer http.ResponseWriter, request *http.Request, providerID string) {
	var body passwordRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	if !handler.passwords.allow(providerID, body.Username, request.RemoteAddr) {
		body.Password = ""
		writeError(writer, http.StatusTooManyRequests, "RATE_LIMITED", "login attempt limit exceeded")
		return
	}
	password := []byte(body.Password)
	body.Password = ""
	result, err := handler.login.AuthenticatePassword(request.Context(), providerID, authn.PasswordCredentials{
		Username: body.Username, Password: password,
	})
	clear(password)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "INVALID_CREDENTIALS", "credentials were rejected")
		return
	}
	handler.passwords.success(providerID, body.Username)
	handler.issue(writer, request, result, body.DeviceID)
}

func (handler *Handler) start(writer http.ResponseWriter, request *http.Request, providerID string) {
	var body startRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := handler.login.Begin(request.Context(), login.BeginRequest{
		ProviderID: providerID, ClientCallback: body.ClientCallback,
		ClientState: body.State, Nonce: body.Nonce, PKCEChallenge: body.PKCEChallenge,
	})
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_LOGIN_REQUEST", "login request was rejected")
		return
	}
	writeJSON(writer, http.StatusOK, startResponse{
		AuthorizationURL: result.AuthorizationURL, ExpiresAt: result.ExpiresAt.Format("2006-01-02T15:04:05.000000000Z"),
	})
}

func (handler *Handler) callback(writer http.ResponseWriter, request *http.Request, providerID string) {
	if request.URL.Query().Get("error") != "" {
		writeBrowserError(writer, http.StatusBadRequest)
		return
	}
	result, err := handler.login.CompleteCallback(request.Context(), login.CallbackRequest{
		ProviderID:    providerID,
		UpstreamCode:  request.URL.Query().Get("code"),
		UpstreamState: request.URL.Query().Get("state"),
	})
	if err != nil {
		writeBrowserError(writer, http.StatusBadRequest)
		return
	}
	http.Redirect(writer, request, result.RedirectURL, http.StatusSeeOther)
}

func (handler *Handler) exchange(writer http.ResponseWriter, request *http.Request) {
	var body exchangeRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	loginResult, err := handler.login.Exchange(request.Context(), body.Code, body.PKCEVerifier)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_EXCHANGE_CODE", "exchange code was rejected")
		return
	}
	pair, err := handler.tokens.Issue(request.Context(), loginResult.Principal, body.DeviceID)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "TOKEN_UNAVAILABLE", "token service is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, tokenPayload(pair))
}

func (handler *Handler) refresh(writer http.ResponseWriter, request *http.Request) {
	var body refreshRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	pair, err := handler.tokens.Refresh(request.Context(), body.RefreshToken)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "INVALID_REFRESH_TOKEN", "refresh token was rejected")
		return
	}
	writeJSON(writer, http.StatusOK, tokenPayload(pair))
}

func (handler *Handler) revoke(writer http.ResponseWriter, request *http.Request) {
	var body refreshRequest
	if !decodeJSON(writer, request, &body) {
		return
	}
	// Revocation is intentionally idempotent at the HTTP boundary so callers do
	// not learn whether a supplied token existed.
	_ = handler.tokens.Revoke(request.Context(), body.RefreshToken)
	writer.WriteHeader(http.StatusNoContent)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxAuthBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid")
		return false
	}
	return true
}

func tokenPayload(pair token.Pair) tokenResponse {
	return tokenResponse{
		TokenType: pair.TokenType, AccessToken: pair.AccessToken,
		AccessExpiresAt:  pair.AccessExpiresAt.Format("2006-01-02T15:04:05.000000000Z"),
		RefreshToken:     pair.RefreshToken,
		RefreshExpiresAt: pair.RefreshExpiresAt.Format("2006-01-02T15:04:05.000000000Z"),
	}
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, errorResponse{Code: code, Message: message})
}

func writeBrowserError(writer http.ResponseWriter, status int) {
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte("KubeLoop login failed. Return to the desktop application and try again.\n"))
}
