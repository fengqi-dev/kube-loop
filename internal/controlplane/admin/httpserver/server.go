// Package httpserver serves the browser Management Plane on an isolated port.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/labstack/echo/v5"
)

const DefaultListenAddress = ":8081"

type Config struct {
	ListenAddress string
}

type Server struct {
	listenAddress string
	http          *http.Server
	cancel        context.CancelFunc
	active        *controlplanemiddleware.RequestTracker
}

func New(
	config Config,
	management http.Handler,
	authRoutes controlplane.RouteRegistrar,
	authMethods controlplane.AuthMethodSource,
	logger *slog.Logger,
) (*Server, error) {
	listenAddress := strings.TrimSpace(config.ListenAddress)
	if listenAddress == "" {
		listenAddress = DefaultListenAddress
	}
	if _, err := net.ResolveTCPAddr("tcp", listenAddress); err != nil {
		return nil, errors.New("management listen address is invalid")
	}
	if management == nil {
		return nil, errors.New("management HTTP handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	router := echo.New()
	router.Use(httpmiddleware.RequestID())
	router.Use(httpmiddleware.RequestLogger(logger))
	router.GET(controlplane.DiscoveryPath, func(ctx *echo.Context) error {
		methods := []controlplane.AuthMethod{}
		if authMethods != nil {
			methods = append(methods, authMethods.AuthMethods()...)
		}
		ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return ctx.JSON(http.StatusOK, map[string]any{"authMethods": methods})
	})
	if authRoutes != nil {
		authRoutes.RegisterRoutes(router.Group(""))
	}
	managementPath := controlplane.APIPathPrefix + "/admin"
	mount(router.Group(managementPath), http.StripPrefix(managementPath, management))

	serverContext, cancel := context.WithCancel(context.Background())
	active := controlplanemiddleware.NewRequestTracker()
	return &Server{
		listenAddress: listenAddress, cancel: cancel, active: active,
		http: &http.Server{
			Handler: active.Middleware(router),
			BaseContext: func(net.Listener) context.Context {
				return serverContext
			},
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    controlplane.DefaultMaxHeaderBytes,
		},
	}, nil
}

func mount(group *echo.Group, handler http.Handler) {
	adapted := echo.WrapHandler(handler)
	group.Any("", adapted)
	group.Any("/*", adapted)
}

func (server *Server) ListenAddress() string { return server.listenAddress }

func (server *Server) Handler() http.Handler { return server.http.Handler }

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
	return errors.Join(server.http.Shutdown(ctx), server.active.Wait(ctx))
}
