// Package httpapi exposes the browser-only Control Plane Management Plane API.
// Its routing and cookie authentication are isolated from the ordinary Gateway
// Bearer API framework.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	adminui "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/ui"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

const (
	SessionCookieName       = "__Host-kubeloop-admin"
	CSRFHeaderName          = "X-KubeLoop-CSRF"
	defaultMaxBodyBytes     = int64(1024)
	defaultGlobalAttempts   = 30
	defaultSourceAttempts   = 5
	defaultTokenGlobal      = 300
	defaultTokenSource      = 30
	defaultRateLimitWindow  = time.Minute
	managementRequestHeader = "X-Request-ID"
)

type Config struct {
	PublicURL           string
	MaxRequestBodyBytes int64
	GlobalAttempts      int
	SourceAttempts      int
	RateLimitWindow     time.Duration
}

type Handler struct {
	sessions   *adminsession.Service
	readAPI    *readAPI
	tokenAuth  TokenAuthenticator
	origin     string
	pathPrefix string
	maxBody    int64
	limiter    *exchangeLimiter
	tokenLimit *exchangeLimiter
}

func New(config Config, sessions *adminsession.Service, optionValues ...Option) (*Handler, error) {
	if sessions == nil {
		return nil, errors.New("management session service is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(config.PublicURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("management public URL is invalid")
	}
	if parsed.Scheme != "https" && !loopbackDevelopmentHost(parsed.Hostname()) {
		return nil, errors.New("management public URL must use HTTPS")
	}
	maxBody := config.MaxRequestBodyBytes
	if maxBody == 0 {
		maxBody = defaultMaxBodyBytes
	}
	globalAttempts := config.GlobalAttempts
	if globalAttempts == 0 {
		globalAttempts = defaultGlobalAttempts
	}
	sourceAttempts := config.SourceAttempts
	if sourceAttempts == 0 {
		sourceAttempts = defaultSourceAttempts
	}
	window := config.RateLimitWindow
	if window == 0 {
		window = defaultRateLimitWindow
	}
	if maxBody < 128 || maxBody > 64<<10 || globalAttempts < 1 || sourceAttempts < 1 ||
		sourceAttempts > globalAttempts || window < time.Second || window > time.Hour {
		return nil, errors.New("management HTTP limits are invalid")
	}
	var options handlerOptions
	for _, option := range optionValues {
		if option != nil {
			if err := option(&options); err != nil {
				return nil, err
			}
		}
	}
	handler := &Handler{
		sessions: sessions, origin: parsed.Scheme + "://" + parsed.Host,
		pathPrefix: controlplane.AdminAPIPathPrefix,
		maxBody:    maxBody, limiter: newExchangeLimiter(globalAttempts, sourceAttempts, window),
		tokenLimit: newExchangeLimiter(defaultTokenGlobal, defaultTokenSource, window),
	}
	if options.readAPI != nil {
		handler.readAPI = options.readAPI
		handler.readAPI.handler = handler
		handler.readAPI.relays = options.relayStatus
		handler.readAPI.policy = options.policyService
		handler.readAPI.reloader = options.policyReloader
		handler.readAPI.providers = options.providerService
		handler.readAPI.operations = options.operationService
		handler.readAPI.localUsers = options.localUsers
	} else if options.relayStatus != nil {
		return nil, errors.New("management Relay status source requires the read API")
	} else if options.policyService != nil || options.policyReloader != nil || options.providerService != nil ||
		options.operationService != nil || options.localUsers != nil {
		return nil, errors.New("management policy API requires the read API")
	}
	if options.tokenAuthenticator != nil {
		if handler.readAPI == nil {
			return nil, errors.New("management token exchange requires the read API authorizer")
		}
		handler.tokenAuth = options.tokenAuthenticator
	}
	return handler, nil
}

func (handler *Handler) RegisterRoutes(group *echo.Group) {
	group.Use(handler.securityHeaders)
	adminui.New(handler.pathPrefix).RegisterRoutes(group.Group("/ui"))
	group.POST("/sessions/break-glass", handler.exchangeBreakGlass)
	if handler.tokenAuth != nil {
		group.POST("/sessions/token", handler.exchangeToken)
	}
	if handler.readAPI != nil {
		handler.readAPI.routes(group)
	}
}

func (handler *Handler) securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx *echo.Context) error {
		writer := ctx.Response()
		request := ctx.Request()
		for _, value := range ctx.PathValues() {
			request.SetPathValue(value.Name, value.Value)
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Pragma", "no-cache")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		return next(ctx)
	}
}

func (handler *Handler) exchangeBreakGlass(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	requestID := ensureRequestID(writer, request)
	source, sourceKey := sourceAddress(request.RemoteAddr)
	if !handler.limiter.allow(sourceKey) {
		writeError(writer, http.StatusTooManyRequests, "rate_limited", "management authentication failed", requestID)
		return nil
	}
	if request.Header.Get("Origin") != handler.origin || request.Header.Get("Authorization") != "" ||
		request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_request", "application/json is required", requestID)
		return nil
	}
	request.Body = http.MaxBytesReader(writer, request.Body, handler.maxBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Credential string `json:"credential"`
	}
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Credential) == "" {
		input.Credential = ""
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request", requestID)
		return nil
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		input.Credential = ""
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request", requestID)
		return nil
	}
	credential := []byte(input.Credential)
	input.Credential = ""
	issued, err := handler.sessions.ExchangeBreakGlass(request.Context(), source, credential, requestID)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return nil
	}
	setSessionCookie(writer, issued)
	writeJSON(writer, http.StatusCreated, map[string]any{
		"csrfToken": issued.CSRFToken,
		"expiresAt": issued.ExpiresAt.Format(time.RFC3339Nano),
		"requestId": requestID,
	})
	return nil
}

func setSessionCookie(writer http.ResponseWriter, issued adminsession.Credentials) {
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: issued.SessionToken, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: max(1, int(time.Until(issued.ExpiresAt).Seconds())),
	})
}

func ensureRequestID(writer http.ResponseWriter, request *http.Request) string {
	requestID := strings.TrimSpace(writer.Header().Get(managementRequestHeader))
	if requestID == "" {
		requestID = strings.TrimSpace(request.Header.Get(managementRequestHeader))
	}
	parsed, err := uuid.Parse(requestID)
	if err != nil || parsed.String() != requestID {
		requestID = uuid.NewString()
	}
	writer.Header().Set(managementRequestHeader, requestID)
	return requestID
}

func sourceAddress(remote string) (netip.Addr, string) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}, "invalid"
	}
	address = address.Unmap()
	return address, address.String()
}

func loopbackDevelopmentHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func writeError(writer http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{
		"code": code, "message": message, "requestId": requestID,
	}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
