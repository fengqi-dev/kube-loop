package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
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

type healthDocument struct {
	Status string `json:"status"`
}

type Server struct {
	config Config
	http   *http.Server
	cancel context.CancelFunc
	active *requestTracker
}

type requestTracker struct {
	mu       sync.Mutex
	active   int
	draining bool
	done     chan struct{}
	closed   bool
}

func newRequestTracker() *requestTracker {
	return &requestTracker{done: make(chan struct{})}
}

func (tracker *requestTracker) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !tracker.begin() {
			writer.Header().Set("Connection", "close")
			http.Error(writer, "server is shutting down", http.StatusServiceUnavailable)
			return
		}
		defer tracker.end()
		next.ServeHTTP(writer, request)
	})
}

func (tracker *requestTracker) begin() bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.draining {
		return false
	}
	tracker.active++
	return true
}

func (tracker *requestTracker) end() {
	tracker.mu.Lock()
	tracker.active--
	tracker.closeDoneLocked()
	tracker.mu.Unlock()
}

func (tracker *requestTracker) drain() {
	tracker.mu.Lock()
	tracker.draining = true
	tracker.closeDoneLocked()
	tracker.mu.Unlock()
}

func (tracker *requestTracker) closeDoneLocked() {
	if tracker.draining && tracker.active == 0 && !tracker.closed {
		tracker.closed = true
		close(tracker.done)
	}
}

func (tracker *requestTracker) wait(ctx context.Context) error {
	select {
	case <-tracker.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	router := chi.NewRouter()
	router.Get(DiscoveryPath, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "public, max-age=60")
		writeJSON(writer, http.StatusOK, discovery)
	})
	router.Get("/health/live", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writeJSON(writer, http.StatusOK, healthDocument{Status: "ok"})
	})
	router.Get("/health/ready", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if options.readiness != nil {
			readinessContext, cancel := context.WithTimeout(request.Context(), normalized.ReadinessTimeout)
			defer cancel()
			if err := options.readiness.Check(readinessContext); err != nil {
				writeJSON(writer, http.StatusServiceUnavailable, healthDocument{Status: "unavailable"})
				return
			}
		}
		writeJSON(writer, http.StatusOK, healthDocument{Status: "ready"})
	})
	if options.authHandler != nil {
		router.Mount("/auth", options.authHandler)
	}
	api := newAPIFramework(normalized, logger, options)
	router.Mount(APIPathPrefix, api)
	serverContext, cancel := context.WithCancel(context.Background())
	active := newRequestTracker()
	return &Server{
		config: normalized,
		cancel: cancel,
		active: active,
		http: &http.Server{
			Handler:           active.handler(requestLog(logger, router)),
			BaseContext:       func(net.Listener) context.Context { return serverContext },
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    DefaultMaxHeaderBytes,
		},
	}, nil
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
	server.active.drain()
	server.cancel()
	httpErr := server.http.Shutdown(ctx)
	waitErr := server.active.wait(ctx)
	return errors.Join(httpErr, waitErr)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		logger.DebugContext(request.Context(), "http request",
			"method", request.Method,
			"path", request.URL.Path,
			"remote_address", request.RemoteAddr,
			"duration", time.Since(started),
		)
	})
}
