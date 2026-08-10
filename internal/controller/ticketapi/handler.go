package ticketapi

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const OperationTunnel = "tunnel"

type SessionValidator interface {
	RequireActive(context.Context, controller.Principal, string, string) (sessionapi.ActiveSession, *controller.APIError)
}

type RelayAllocator interface {
	Allocate(relaycontrol.AllocationRequest) (relaycontrol.AllocationResponse, error)
}

type Config struct {
	Issuer    string
	Audience  string
	TTL       time.Duration
	Now       func() time.Time
	Signer    *relayticket.Signer
	Allocator RelayAllocator
	Topology  map[string]string
}

type Handler struct {
	sessions  SessionValidator
	issuer    string
	audience  string
	ttl       time.Duration
	now       func() time.Time
	signer    *relayticket.Signer
	allocator RelayAllocator
	topology  map[string]string
}

type issueRequest struct{}

type issueResponse struct {
	TokenType string    `json:"tokenType"`
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
	DeviceID  string    `json:"deviceId"`
	RelayID   string    `json:"relayId,omitempty"`
	Endpoint  string    `json:"endpoint,omitempty"`
}

func New(sessions SessionValidator, config Config) (*Handler, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	config.Audience = strings.TrimSpace(config.Audience)
	if sessions == nil || config.Signer == nil || config.Issuer == "" || len(config.Issuer) > 512 ||
		(config.Allocator == nil && (config.Audience == "" || len(config.Audience) > 128)) {
		return nil, errors.New("RelayTicket API configuration is invalid")
	}
	if config.TTL == 0 {
		config.TTL = relayticket.DefaultLifetime
	}
	if config.TTL < 15*time.Second || config.TTL > relayticket.MaximumLifetime {
		return nil, errors.New("RelayTicket TTL must be between 15 seconds and 2 minutes")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Handler{
		sessions: sessions, issuer: config.Issuer, audience: config.Audience,
		ttl: config.TTL, now: config.Now, signer: config.Signer, allocator: config.Allocator,
		topology: cloneTopology(config.Topology),
	}, nil
}

func (handler *Handler) ServeAPI(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
) *controller.APIError {
	sessionID := chi.URLParam(request, "sessionID")
	if _, err := uuid.Parse(sessionID); err != nil {
		return notFound()
	}
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return apiError
	}
	var body issueRequest
	if apiError := controller.DecodeJSON(request, &body); apiError != nil {
		return apiError
	}
	session, apiError := handler.sessions.RequireActive(request.Context(), principal, namespace, sessionID)
	if apiError != nil {
		return apiError
	}
	controller.SetAuditSessionID(request.Context(), session.ID)
	now := handler.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(handler.ttl)
	if session.ExpiresAt.Before(expiresAt) {
		expiresAt = session.ExpiresAt.UTC().Truncate(time.Second)
	}
	if !expiresAt.After(now.Add(5 * time.Second)) {
		return &controller.APIError{Code: controller.CodeConflict, Message: "Session expires too soon to issue a RelayTicket"}
	}
	audience := handler.audience
	var assignment relaycontrol.AllocationResponse
	if handler.allocator != nil {
		allocation := relaycontrol.NewAllocationRequest()
		allocation.SessionID = session.ID
		allocation.Generation = session.Generation
		allocation.NetworkSpecHash = session.NetworkSpecHash
		allocation.Topology = cloneTopology(handler.topology)
		var err error
		assignment, err = handler.allocator.Allocate(allocation)
		if err != nil {
			return &controller.APIError{
				Code: controller.CodeUnavailable, Message: "No ready Data Plane is available", Cause: err,
			}
		}
		audience = assignment.RelayID
	}
	claims := relayticket.Claims{
		Version: relayticket.Version, Issuer: handler.issuer, Audience: audience,
		PrincipalID: principal.Subject, DeviceID: principal.DeviceID,
		SessionID: session.ID, SessionGeneration: session.Generation,
		Namespace: session.Namespace, Operations: []string{OperationTunnel},
		NetworkSpecHash: session.NetworkSpecHash, TicketID: uuid.NewString(),
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: expiresAt.Unix(),
	}
	ticket, err := handler.signer.Sign(claims)
	if err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "RelayTicket issuance failed", Cause: err}
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(writer).Encode(issueResponse{
		TokenType: relayticket.Type, Ticket: ticket, ExpiresAt: expiresAt,
		DeviceID: principal.DeviceID, RelayID: assignment.RelayID, Endpoint: assignment.Endpoint,
	})
	return nil
}

func cloneTopology(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	maps.Copy(result, source)
	return result
}

func namespaceFromQuery(request *http.Request) (string, *controller.APIError) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" {
			return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: key, Message: "query parameter is not supported"}
		}
		if len(values) != 1 {
			return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once"}
		}
	}
	namespace := query.Get("namespace")
	if namespace == "" || len(namespace) > 63 {
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: "namespace", Message: "namespace is invalid"}
	}
	for index, character := range namespace {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(character == '-' && index > 0 && index < len(namespace)-1) {
			continue
		}
		return "", &controller.APIError{Code: controller.CodeInvalidArgument, Field: "namespace", Message: "namespace is invalid"}
	}
	return namespace, nil
}

func notFound() *controller.APIError {
	return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
}
