// Package httpapi exposes the browser-only Controller Management Plane API.
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

	adminsession "github.com/fengqi-dev/kube-loop/internal/controller/admin/session"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	SessionCookieName       = "__Host-kubeloop-admin"
	CSRFHeaderName          = "X-KubeLoop-CSRF"
	defaultMaxBodyBytes     = int64(1024)
	defaultGlobalAttempts   = 30
	defaultSourceAttempts   = 5
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
	sessions *adminsession.Service
	readAPI  *readAPI
	origin   string
	maxBody  int64
	limiter  *exchangeLimiter
	router   http.Handler
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
		maxBody: maxBody, limiter: newExchangeLimiter(globalAttempts, sourceAttempts, window),
	}
	if options.readAPI != nil {
		handler.readAPI = options.readAPI
		handler.readAPI.handler = handler
	}
	router := chi.NewRouter()
	router.Use(handler.securityHeaders)
	router.Post("/sessions/break-glass", handler.exchangeBreakGlass)
	if handler.readAPI != nil {
		router.Group(handler.readAPI.routes)
	}
	handler.router = router
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.router.ServeHTTP(writer, request)
}

func (handler *Handler) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Pragma", "no-cache")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(writer, request)
	})
}

func (handler *Handler) exchangeBreakGlass(writer http.ResponseWriter, request *http.Request) {
	requestID := uuid.NewString()
	writer.Header().Set(managementRequestHeader, requestID)
	source, sourceKey := sourceAddress(request.RemoteAddr)
	if !handler.limiter.allow(sourceKey) {
		writeError(writer, http.StatusTooManyRequests, "rate_limited", "management authentication failed", requestID)
		return
	}
	if request.Header.Get("Origin") != handler.origin || request.Header.Get("Authorization") != "" ||
		request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_request", "application/json is required", requestID)
		return
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
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		input.Credential = ""
		writeError(writer, http.StatusBadRequest, "invalid_request", "invalid request", requestID)
		return
	}
	credential := []byte(input.Credential)
	input.Credential = ""
	issued, err := handler.sessions.ExchangeBreakGlass(request.Context(), source, credential, requestID)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID)
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: issued.SessionToken, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: max(1, int(time.Until(issued.ExpiresAt).Seconds())),
	})
	writeJSON(writer, http.StatusCreated, map[string]any{
		"csrfToken": issued.CSRFToken,
		"expiresAt": issued.ExpiresAt.Format(time.RFC3339Nano),
		"requestId": requestID,
	})
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
