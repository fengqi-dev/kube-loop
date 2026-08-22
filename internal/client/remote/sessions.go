package remote

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

func (client *Client) CreateSession(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace, idempotencyKey string,
) (Session, error) {
	if err := validateNamespace(namespace); err != nil {
		return Session{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return Session{}, errors.New("session idempotency key is required")
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodPost, "/api/sessions", url.Values{remoteParamNamespace: {namespace}},
		http.Header{remoteHeaderIdempotencyKey: {idempotencyKey}}, &result,
	); err != nil {
		return Session{}, err
	}
	result, err := validateSession(result, namespace)
	if err != nil {
		return Session{}, err
	}
	if result.Capabilities == nil {
		return Session{}, errors.New("gateway returned a Session without capabilities")
	}
	snapshot, err := capability.Normalize(*result.Capabilities)
	if err != nil || snapshot.Namespace != namespace {
		return Session{}, errors.New("gateway returned an invalid Session capability binding")
	}
	result.Capabilities = &snapshot
	// Session creation may rotate credentials. Cache the snapshot only under the
	// authentication scope that received the successful response.
	if scope, scopeErr := client.capabilityAuthScope(serverProfile); scopeErr == nil {
		client.storeCapabilities(scope, snapshot)
	}
	return result, nil
}

func (client *Client) GetSession(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace, sessionID string,
) (Session, error) {
	if err := validateSessionTarget(namespace, sessionID); err != nil {
		return Session{}, err
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodGet, "/api/sessions/"+url.PathEscape(sessionID),
		url.Values{remoteParamNamespace: {namespace}}, nil, &result,
	); err != nil {
		return Session{}, err
	}
	return validateSession(result, namespace)
}

func (client *Client) HeartbeatSession(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) (Session, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.Generation == 0 {
		return Session{}, errors.New("current Session identity and generation are required")
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodPost, "/api/sessions/"+url.PathEscape(current.ID)+"/heartbeat",
		url.Values{remoteParamNamespace: {current.Namespace}}, generationHeader(current.Generation), &result,
	); err != nil {
		return Session{}, err
	}
	result, err := validateSession(result, current.Namespace)
	if err != nil {
		return Session{}, err
	}
	result.Capabilities = cloneCapabilityPointer(current.Capabilities)
	return result, nil
}

func (client *Client) DisconnectSession(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) (Session, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.Generation == 0 {
		return Session{}, errors.New("current Session identity and generation are required")
	}
	var result Session
	if err := client.doJSON(
		ctx, serverProfile, http.MethodDelete, "/api/sessions/"+url.PathEscape(current.ID),
		url.Values{remoteParamNamespace: {current.Namespace}}, generationHeader(current.Generation), &result,
	); err != nil {
		return Session{}, err
	}
	result, err := validateSession(result, current.Namespace)
	if err != nil {
		return Session{}, err
	}
	result.Capabilities = cloneCapabilityPointer(current.Capabilities)
	return result, nil
}

func (client *Client) IssueRelayTicket(
	ctx context.Context,
	serverProfile profile.Profile,
	current Session,
) (RelayTicket, error) {
	if err := validateSessionTarget(current.Namespace, current.ID); err != nil || current.State != remoteSessionActive {
		return RelayTicket{}, errors.New("active Session identity is required")
	}
	body := []byte(`{}`)
	var result RelayTicket
	if err := client.doJSONBody(
		ctx, serverProfile, http.MethodPost,
		"/api/sessions/"+url.PathEscape(current.ID)+"/tickets",
		url.Values{remoteParamNamespace: {current.Namespace}}, nil, body, &result,
	); err != nil {
		return RelayTicket{}, err
	}
	invalidTicket := result.TokenType != relayticket.Type || result.Ticket == ""
	invalidTicket = invalidTicket || len(result.Ticket) > relayticket.MaximumTicketBytes
	invalidTicket = invalidTicket || strings.TrimSpace(result.Ticket) != result.Ticket
	if invalidTicket || !result.ExpiresAt.After(client.now()) ||
		!validRelayDeviceID(result.DeviceID) ||
		!validRelayAssignment(result.RelayID, result.Endpoint) {
		return RelayTicket{}, errors.New("gateway returned an invalid RelayTicket")
	}
	return result, nil
}

func validRelayDeviceID(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
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

func validRelayAssignment(relayID, endpoint string) bool {
	if relayID == "" && endpoint == "" {
		return true
	}
	if !strings.HasPrefix(relayID, "relay-") || len(relayID) != len("relay-")+64 {
		return false
	}
	for _, character := range relayID[len("relay-"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	parsed, err := url.Parse(endpoint)
	validScheme := parsed.Scheme == "ws" || parsed.Scheme == remoteWSSScheme
	validAuthority := parsed.Host != "" && parsed.User == nil
	validPath := parsed.Path != "" && strings.TrimRight(parsed.Path, "/") == parsed.Path
	return err == nil && validScheme && validAuthority && validPath && parsed.RawQuery == "" && parsed.Fragment == ""
}
