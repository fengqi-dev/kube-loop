package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	adminui "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/ui"
)

const (
	SessionCookieName       = "__Host-kubeloop-admin"
	CSRFCookieName          = "__Host-kubeloop-admin-csrf"
	CSRFHeaderName          = "X-Kubeloop-Csrf"
	httpSessionCookieName   = "kubeloop-admin"
	httpCSRFCookieName      = "kubeloop-admin-csrf"
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
	sessions          *adminsession.Service
	readAPI           *readAPI
	tokenAuth         TokenAuthenticator
	origin            string
	secureCookies     bool
	sessionCookieName string
	csrfCookieName    string
	pathPrefix        string
	maxBody           int64
	limiter           *exchangeLimiter
	tokenLimit        *exchangeLimiter
}

func New(
	config Config,
	sessions *adminsession.Service,
	optionValues ...Option,
) (*Handler, error) {
	if sessions == nil {
		return nil, errors.New("management session service is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(config.PublicURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return nil, errors.New("management public URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != schemeHTTPS {
		return nil, errors.New("management public URL must use HTTP or HTTPS")
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
		sourceAttempts > globalAttempts ||
		window < time.Second ||
		window > time.Hour {
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
		pathPrefix: controlplane.AdminPathPrefix,
		maxBody:    maxBody, limiter: newExchangeLimiter(globalAttempts, sourceAttempts, window),
		tokenLimit: newExchangeLimiter(
			defaultTokenGlobal,
			defaultTokenSource,
			window,
		),
	}
	if parsed.Scheme == schemeHTTPS {
		handler.secureCookies = true
		handler.sessionCookieName = SessionCookieName
		handler.csrfCookieName = CSRFCookieName
	} else {
		handler.sessionCookieName = httpSessionCookieName
		handler.csrfCookieName = httpCSRFCookieName
	}
	if options.readAPI != nil {
		handler.readAPI = options.readAPI
		handler.readAPI.handler = handler
		handler.readAPI.oauthRepositories = options.oauthRepositories
		handler.readAPI.oauthTransactions = options.oauthTransactions
		handler.readAPI.localUsers = options.localUsers
		handler.readAPI.bootstrapService = options.bootstrapService
	} else if options.localUsers != nil {
		return nil, errors.New("management API services require the read API")
	}
	if options.tokenAuthenticator != nil {
		if handler.readAPI == nil {
			return nil, errors.New(
				"management token exchange requires the read API",
			)
		}
		handler.tokenAuth = options.tokenAuthenticator
	}
	return handler, nil
}

func (handler *Handler) RegisterRoutes(group *echo.Group) {
	group.Use(handler.securityHeaders)
	adminui.New(handler.pathPrefix).RegisterRoutes(group.Group("/ui"))
	if handler.tokenAuth != nil {
		group.POST(
			"/sessions/token",
			handler.exchangeToken,
			middleware.BodyLimit(handler.maxBody),
		)
	}
	if handler.readAPI != nil {
		handler.readAPI.routes(group)
	}
}

func (handler *Handler) securityHeaders(
	next echo.HandlerFunc,
) echo.HandlerFunc {
	return func(ctx *echo.Context) error {
		header := ctx.Response().Header()
		header.Set("Cache-Control", "no-store")
		header.Set("Pragma", "no-cache")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set(
			"Content-Security-Policy",
			"default-src 'none'; frame-ancestors 'none'; base-uri 'none'",
		)
		header.Set("X-Frame-Options", "DENY")
		header.Set(
			"Permissions-Policy",
			"camera=(), microphone=(), geolocation=(), publickey-credentials-create=(), publickey-credentials-get=()",
		)
		return next(ctx)
	}
}

func (handler *Handler) setSessionCookies(
	ctx *echo.Context,
	issued adminsession.Credentials,
) {
	//nolint:gosec // Secure follows the validated public URL; local HTTP development intentionally disables it.
	ctx.SetCookie(&http.Cookie{
		Name:     handler.sessionCookieName,
		Value:    issued.SessionToken,
		Path:     "/",
		Secure:   handler.secureCookies,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   max(1, int(time.Until(issued.ExpiresAt).Seconds())),
	})
	//nolint:gosec // This double-submit CSRF cookie must remain script-readable; Secure follows the public URL.
	ctx.SetCookie(&http.Cookie{
		Name: handler.csrfCookieName, Value: issued.CSRFToken, Path: "/", Secure: handler.secureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: max(1, int(time.Until(issued.ExpiresAt).Seconds())),
	})
}

func ensureRequestID(ctx *echo.Context) string {
	header := ctx.Response().Header()
	requestID := strings.TrimSpace(header.Get(managementRequestHeader))
	if requestID == "" {
		requestID = strings.TrimSpace(
			ctx.Request().Header.Get(managementRequestHeader),
		)
	}
	parsed, err := uuid.Parse(requestID)
	if err != nil || parsed.String() != requestID {
		requestID = uuid.NewString()
	}
	header.Set(managementRequestHeader, requestID)
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

func writeError(
	ctx *echo.Context,
	status int,
	code, message, requestID string,
) error {
	return ctx.JSON(status, map[string]any{"error": map[string]string{
		"code": code, "message": message, "requestId": requestID,
	}})
}
