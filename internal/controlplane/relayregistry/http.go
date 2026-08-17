package relayregistry

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/labstack/echo/v5"
)

const InternalPathPrefix = "/internal/v1/relays"

type Authenticator interface {
	Authenticate(*http.Request) (relaycontrol.PeerIdentity, error)
}

type TopologyResolver func(context.Context, relaycontrol.PeerIdentity) (map[string]string, error)

type MTLSConfig struct {
	TrustDomain      string
	Namespace        string
	ServiceAccount   string
	TopologyResolver TopologyResolver
}

type MTLSAuthenticator struct{ config MTLSConfig }

func NewMTLSAuthenticator(config MTLSConfig) (*MTLSAuthenticator, error) {
	probe := relaycontrol.PeerIdentity{
		TrustDomain: config.TrustDomain, Namespace: config.Namespace,
		ServiceAccount: config.ServiceAccount, PodUID: "probe",
	}
	if err := probe.Validate(); err != nil {
		return nil, errors.New("Relay mTLS identity configuration is invalid")
	}
	return &MTLSAuthenticator{config: config}, nil
}

func (authenticator *MTLSAuthenticator) Authenticate(request *http.Request) (relaycontrol.PeerIdentity, error) {
	if authenticator == nil || request == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
		return relaycontrol.PeerIdentity{}, errors.New("verified Relay client certificate is required")
	}
	identity, err := authenticator.identityFromChains(request.TLS.VerifiedChains)
	if err != nil {
		return relaycontrol.PeerIdentity{}, err
	}
	if authenticator.config.TopologyResolver != nil {
		topology, err := authenticator.config.TopologyResolver(request.Context(), identity)
		if err != nil {
			return relaycontrol.PeerIdentity{}, errors.New("resolve authenticated Relay topology")
		}
		identity.Topology = topology
	}
	if err := identity.Validate(); err != nil {
		return relaycontrol.PeerIdentity{}, err
	}
	return identity, nil
}

func (authenticator *MTLSAuthenticator) identityFromChains(chains [][]*x509.Certificate) (relaycontrol.PeerIdentity, error) {
	for _, chain := range chains {
		if len(chain) == 0 || chain[0] == nil {
			continue
		}
		for _, identityURI := range chain[0].URIs {
			if identityURI == nil || identityURI.Scheme != "spiffe" ||
				!strings.EqualFold(identityURI.Host, authenticator.config.TrustDomain) {
				continue
			}
			segments := strings.Split(strings.Trim(identityURI.EscapedPath(), "/"), "/")
			if len(segments) != 6 || segments[0] != "ns" || segments[2] != "sa" || segments[4] != "pod" {
				continue
			}
			identity := relaycontrol.PeerIdentity{
				TrustDomain: identityURI.Host, Namespace: segments[1],
				ServiceAccount: segments[3], PodUID: segments[5],
			}
			if identity.Namespace != authenticator.config.Namespace ||
				identity.ServiceAccount != authenticator.config.ServiceAccount {
				continue
			}
			if err := identity.Validate(); err == nil {
				return identity, nil
			}
		}
	}
	return relaycontrol.PeerIdentity{}, errors.New("Relay client certificate has no allowed workload identity")
}

type HTTPHandler struct {
	registry      *Registry
	authenticator Authenticator
	router        *echo.Echo
}

func NewHTTPHandler(registry *Registry, authenticator Authenticator, logger *slog.Logger) (*HTTPHandler, error) {
	if registry == nil || authenticator == nil {
		return nil, errors.New("Relay Registry and authenticator are required")
	}
	handler := &HTTPHandler{registry: registry, authenticator: authenticator}
	router := echo.New()
	router.Use(httpmiddleware.RequestID())
	router.Use(httpmiddleware.RequestLogger(logger))
	group := router.Group(InternalPathPrefix)
	group.POST("/register", handler.register)
	group.PUT("/heartbeat", handler.heartbeat)
	handler.router = router
	return handler, nil
}

func (handler *HTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.router.ServeHTTP(writer, request)
}

// Mount adds authenticated internal APIs to the Relay control server. It must
// be called during startup, before ServeHTTP is used.
func (handler *HTTPHandler) Mount(routes interface{ RegisterRoutes(*echo.Echo) }) error {
	if handler == nil || handler.router == nil || routes == nil {
		return errors.New("internal API routes are required")
	}
	routes.RegisterRoutes(handler.router)
	return nil
}

func (handler *HTTPHandler) register(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	identity, err := handler.authenticator.Authenticate(request)
	if err != nil {
		writeInternalError(writer, http.StatusUnauthorized, "unauthenticated", "Relay workload identity is invalid")
		return nil
	}
	raw, err := readInternalBody(request)
	if err != nil {
		writeInternalError(writer, http.StatusBadRequest, "invalid_argument", "Relay registration body is invalid")
		return nil
	}
	now := handler.registry.config.Now().UTC()
	document, err := relaycontrol.DecodeRegistrationRequest(raw, now)
	if err != nil {
		writeInternalError(writer, http.StatusBadRequest, "invalid_argument", "Relay registration is invalid")
		return nil
	}
	response, err := handler.registry.Register(identity, document)
	if err != nil {
		writeRegistryError(writer, err)
		return nil
	}
	writeInternalDocument(writer, http.StatusCreated, response, now)
	return nil
}

func (handler *HTTPHandler) heartbeat(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	identity, err := handler.authenticator.Authenticate(request)
	if err != nil {
		writeInternalError(writer, http.StatusUnauthorized, "unauthenticated", "Relay workload identity is invalid")
		return nil
	}
	raw, err := readInternalBody(request)
	if err != nil {
		writeInternalError(writer, http.StatusBadRequest, "invalid_argument", "Relay heartbeat body is invalid")
		return nil
	}
	now := handler.registry.config.Now().UTC()
	document, err := relaycontrol.DecodeHeartbeatRequest(raw, now)
	if err != nil {
		writeInternalError(writer, http.StatusBadRequest, "invalid_argument", "Relay heartbeat is invalid")
		return nil
	}
	response, err := handler.registry.Heartbeat(identity, document)
	if err != nil {
		writeRegistryError(writer, err)
		return nil
	}
	writeInternalDocument(writer, http.StatusOK, response, now)
	return nil
}

func readInternalBody(request *http.Request) ([]byte, error) {
	if request.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("content type must be application/json")
	}
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, relaycontrol.MaximumBodyBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > relaycontrol.MaximumBodyBytes {
		return nil, errors.New("Relay control body size is invalid")
	}
	return raw, nil
}

func writeInternalDocument[T interface {
	Validate(time.Time) error
}](writer http.ResponseWriter, status int, document T, now time.Time) {
	raw, err := relaycontrol.Encode(document, now)
	if err != nil {
		writeInternalError(writer, http.StatusInternalServerError, "internal", "Relay control response failed")
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(raw)
}

func writeRegistryError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeInternalError(writer, http.StatusNotFound, "not_found", "Relay lease was not found")
	case errors.Is(err, ErrConflict):
		writeInternalError(writer, http.StatusConflict, "conflict", "Relay lease does not match")
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrAssignedRelayUnavailable):
		writeInternalError(writer, http.StatusServiceUnavailable, "unavailable", "Relay is unavailable")
	default:
		writeInternalError(writer, http.StatusBadRequest, "invalid_argument", "Relay control request is invalid")
	}
}

func writeInternalError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}
