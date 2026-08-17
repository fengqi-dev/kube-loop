package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/labstack/echo/v5"
)

const (
	DiscoveryPath          = "/.well-known/kubeloop"
	DefaultProtocolVersion = "2.0"
)

type BuildInfo struct {
	Version     string
	Commit      string
	ProtocolMin string
	ProtocolMax string
}

type AuthMethod struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"displayName,omitempty"`
	Interaction string `json:"interaction"`
}

type DiscoveryDocument struct {
	ServiceID        string       `json:"serviceId"`
	PublicURL        string       `json:"publicUrl"`
	TunnelPath       string       `json:"tunnelPath"`
	APIVersions      []string     `json:"apiVersions"`
	AuthMethods      []AuthMethod `json:"authMethods"`
	Features         []string     `json:"features"`
	ServerVersion    string       `json:"serverVersion"`
	ServerCommit     string       `json:"serverCommit,omitempty"`
	ProtocolMin      string       `json:"protocolMin"`
	ProtocolMax      string       `json:"protocolMax"`
	MinClientVersion string       `json:"minClientVersion,omitempty"`
}

type Server struct {
	config Config
	http   *http.Server
	cancel context.CancelFunc
	active *controlplanemiddleware.RequestTracker
}

func NewServer(config Config, build BuildInfo, logger *slog.Logger, serverOptionValues ...ServerOption) (*Server, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	if build.Version == "" {
		build.Version = "dev"
	}
	if build.ProtocolMin == "" {
		build.ProtocolMin = DefaultProtocolVersion
	}
	if build.ProtocolMax == "" {
		build.ProtocolMax = DefaultProtocolVersion
	}
	var options serverOptions
	for _, option := range serverOptionValues {
		if option != nil {
			option(&options)
		}
	}
	discovery := DiscoveryDocument{
		ServiceID:        normalized.ServiceID,
		PublicURL:        normalized.PublicURL,
		TunnelPath:       normalized.TunnelPath,
		APIVersions:      []string{"v2"},
		AuthMethods:      append([]AuthMethod(nil), normalized.AuthMethods...),
		Features:         []string{},
		ServerVersion:    build.Version,
		ServerCommit:     build.Commit,
		ProtocolMin:      build.ProtocolMin,
		ProtocolMax:      build.ProtocolMax,
		MinClientVersion: normalized.MinClientVersion,
	}
	router := echo.New()
	defaultHTTPErrorHandler := echo.DefaultHTTPErrorHandler(false)
	router.HTTPErrorHandler = func(ctx *echo.Context, err error) {
		if errors.Is(err, echo.ErrMethodNotAllowed) {
			allow := strings.ReplaceAll(ctx.Response().Header().Get(echo.HeaderAllow), http.MethodOptions+", ", "")
			ctx.Response().Header().Set(echo.HeaderAllow, allow)
		}
		defaultHTTPErrorHandler(ctx, err)
	}
	router.Use(httpmiddleware.RequestID())
	router.Use(httpmiddleware.RequestLogger(logger))
	router.GET(DiscoveryPath, func(ctx *echo.Context) error {
		writer := ctx.Response()
		document := discovery
		document.AuthMethods = append([]AuthMethod(nil), discovery.AuthMethods...)
		if options.authMethodSource != nil {
			document.AuthMethods = append([]AuthMethod(nil), options.authMethodSource.AuthMethods()...)
			writer.Header().Set("Cache-Control", "public, max-age=5")
		} else {
			writer.Header().Set("Cache-Control", "public, max-age=60")
		}
		return ctx.JSON(http.StatusOK, document)
	})
	registerHealthRoutes(router, health.New(options.readiness, normalized.ReadinessTimeout))
	if options.authRoutes != nil {
		options.authRoutes.RegisterRoutes(router.Group(""))
	}
	// The Management Plane is served by its own listener. Register explicit
	// public-listener exclusions before the API group so its authentication
	// middleware cannot turn these requests into a misleading 401 response.
	router.Any(AdminAPIPathPrefix, func(ctx *echo.Context) error {
		return echo.ErrNotFound
	})
	router.Any(AdminAPIPathPrefix+"/*", func(ctx *echo.Context) error {
		return echo.ErrNotFound
	})
	apiGroup := router.Group(APIPathPrefix)
	apiGroup.Use(apiMiddleware(APIPathPrefix, normalized, logger, options))
	if options.apiRoutes != nil {
		options.apiRoutes.RegisterRoutes(apiGroup)
	}
	if sessionRoutes, ok := options.apiRoutes.(SessionRouteRegistrar); ok {
		sessionGroup := router.Group(SessionAPIPathPrefix)
		sessionGroup.Use(apiMiddleware(SessionAPIPathPrefix, normalized, logger, options))
		sessionRoutes.RegisterSessionRoutes(sessionGroup)
	}
	serverContext, cancel := context.WithCancel(context.Background())
	active := controlplanemiddleware.NewRequestTracker()
	return &Server{
		config: normalized,
		cancel: cancel,
		active: active,
		http: &http.Server{
			Handler:           active.Middleware(router),
			BaseContext:       func(net.Listener) context.Context { return serverContext },
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    DefaultMaxHeaderBytes,
		},
	}, nil
}

func apiMiddleware(prefix string, config Config, logger *slog.Logger, options serverOptions) echo.MiddlewareFunc {
	return controlplanemiddleware.New(controlplanemiddleware.Config{
		APIPathPrefix:      prefix,
		RequestTimeout:     config.APIRequestTimeout,
		MaxRequestBodySize: config.MaxRequestBodyBytes,
		Logger:             logger,
		Authenticator:      options.authenticator,
		Authorizer:         options.authorizer,
		Audit:              options.audit,
	})
}

func (server *Server) ListenAddress() string {
	return server.config.ListenAddress
}

func (server *Server) Handler() http.Handler {
	return server.http.Handler
}

func (server *Server) Serve(listener net.Listener) error {
	err := server.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (server *Server) Shutdown(ctx context.Context) error {
	server.active.BeginDrain()
	server.cancel()
	httpErr := server.http.Shutdown(ctx)
	waitErr := server.active.Wait(ctx)
	return errors.Join(httpErr, waitErr)
}
