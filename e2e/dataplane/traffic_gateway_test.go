//go:build e2e

package dataplane

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	ticketservice "github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficcontrolapi"
	"github.com/fengqi-dev/kube-loop/internal/gateway"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/labstack/echo/v5"
)

const (
	e2eTrafficKeyID          = "e2e-traffic"
	e2eTrafficIssuer         = "https://controlplane.e2e.invalid"
	e2eTrafficControlPlaneID = "kubeloop-e2e-dataplane"
)

type e2eTrafficGateway struct {
	tickets     *ticketservice.Service
	httpClient  *http.Client
	certificate *x509.Certificate
}

func startE2EControlPlaneServer(
	t *testing.T,
	handler http.Handler,
	gateway e2eTrafficGateway,
) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	transport, ok := gateway.httpClient.Transport.(*http.Transport)
	if !ok || gateway.certificate == nil {
		server.Close()
		t.Fatal("e2e Gateway does not expose its TLS certificate")
	}
	transport = transport.Clone()
	trustedRoots := x509.NewCertPool()
	trustedRoots.AddCert(gateway.certificate)
	trustedRoots.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    trustedRoots,
	}
	client := *gateway.httpClient
	client.Transport = transport
	return server, &client
}

func TestE2EControlPlaneClientTrustsControlPlaneAndGateway(t *testing.T) {
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer gateway.Close()
	controlPlane, client := startE2EControlPlaneServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}), e2eTrafficGateway{httpClient: gateway.Client(), certificate: gateway.Certificate()})
	defer controlPlane.Close()
	for _, endpoint := range []string{gateway.URL, controlPlane.URL} {
		response, err := client.Get(endpoint)
		if err != nil {
			t.Fatalf("GET %s: %v", endpoint, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("GET %s status = %d, want %d", endpoint, response.StatusCode, http.StatusNoContent)
		}
	}
}

type e2eTrafficSessionSource struct {
	client  *remote.Client
	profile profile.Profile
	session remote.Session
}

func (source *e2eTrafficSessionSource) RelayTicketSource(profileID string) func(context.Context) (remote.RelayTicket, error) {
	return func(ctx context.Context) (remote.RelayTicket, error) {
		if profileID != source.profile.ID {
			return remote.RelayTicket{}, errors.New("e2e Data Plane Profile does not match")
		}
		return source.client.IssueRelayTicket(ctx, source.profile, source.session)
	}
}

func (source *e2eTrafficSessionSource) Current(profileID string) (remote.Session, error) {
	if profileID != source.profile.ID {
		return remote.Session{}, errors.New("e2e Data Plane Profile does not match")
	}
	return source.session, nil
}

func (source *e2eTrafficSessionSource) Refresh(context.Context, string) (remote.Session, error) {
	return source.session, nil
}

func startE2EDataPlane(
	t *testing.T,
	ctx context.Context,
	client *remote.Client,
	gatewayClient *http.Client,
	serverProfile profile.Profile,
	session remote.Session,
) *clientdataplane.Manager {
	return startE2EDataPlaneWithInspection(
		t, ctx, client, gatewayClient, serverProfile, session, clientdataplane.TrafficInspectionConfig{},
	)
}

func startE2EDataPlaneWithInspection(
	t *testing.T,
	ctx context.Context,
	client *remote.Client,
	gatewayClient *http.Client,
	serverProfile profile.Profile,
	session remote.Session,
	inspection clientdataplane.TrafficInspectionConfig,
) *clientdataplane.Manager {
	t.Helper()
	transport, ok := gatewayClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		t.Fatal("e2e Gateway HTTP client does not expose its TLS configuration")
	}
	sessions := &e2eTrafficSessionSource{client: client, profile: serverProfile, session: session}
	manager, err := clientdataplane.NewManager(sessions, clientdataplane.Config{
		ListenAddress: "127.0.0.1:0", ClientVersion: "e2e", TLSConfig: transport.TLSClientConfig.Clone(),
		TrafficInspection: inspection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Connect(ctx, serverProfile, session); err != nil {
		t.Fatalf("connect e2e Data Plane: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Shutdown(); err != nil {
			t.Logf("shutdown e2e Data Plane: %v", err)
		}
	})
	return manager
}

func startE2ETrafficGateway(
	t *testing.T,
	gatewayIP string,
	coordinator trafficcontrolapi.Coordinator,
	mirrorDial func(context.Context, string, string) (net.Conn, error),
) e2eTrafficGateway {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	relayID := "relay-" + strings.Repeat("1", 64)
	router := echo.New()
	server := httptest.NewTLSServer(router)
	t.Cleanup(server.Close)
	endpoint := "wss" + strings.TrimPrefix(server.URL, "https") + "/gateway"

	signer, err := relayticket.NewSigner(e2eTrafficKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	tickets, err := ticketservice.New(ticketservice.Config{
		Issuer: e2eTrafficIssuer,
		Signer: signer,
		Allocator: e2eTrafficAllocator{response: relaycontrol.AllocationResponse{
			RelayID: relayID, Endpoint: endpoint, AssignedAt: time.Now().UTC(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys:   map[string]ed25519.PublicKey{e2eTrafficKeyID: publicKey},
		Issuer: e2eTrafficIssuer, Audience: relayID, RequiredOperation: ticketservice.OperationTunnel,
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := relayticket.NewReplayGuard(128, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	requestVerifier, err := relayticket.NewRequestVerifier(verifier, replay)
	if err != nil {
		t.Fatal(err)
	}
	control := &e2eTrafficControlClient{t: t, relayID: relayID, coordinator: coordinator}
	api, err := trafficapi.New(trafficapi.Config{
		GatewayIP: gatewayIP, ControlPlane: control,
		MirrorPrimaryDialContext: mirrorDial,
	})
	if err != nil {
		t.Fatal(err)
	}
	core := gateway.NewServer(nil, 10*time.Second)
	core.SetTrafficHandler(api)
	tunnelHandler, err := websocketmux.NewHandler(websocketmux.ServerConfig{
		Authenticator: websocketmux.AuthenticatorFunc(func(request *http.Request) (websocketmux.Identity, error) {
			claims, verifyErr := requestVerifier.Verify(request)
			if verifyErr != nil {
				return websocketmux.Identity{}, verifyErr
			}
			return websocketmux.Identity{
				IdentityID: claims.IdentityID, Groups: append([]string(nil), claims.Groups...), DeviceID: claims.DeviceID,
				SessionID: claims.SessionID, SessionGeneration: claims.SessionGeneration, Namespace: claims.Namespace,
				NetworkSpecHash: claims.NetworkSpecHash, ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
			}, nil
		}),
		ServerVersion: "e2e",
		Handle: func(ctx context.Context, identity websocketmux.Identity, connection net.Conn) {
			core.ServeConnForAuthorizationContext(ctx, connection, gateway.SessionAuthorization{
				RequestID: identity.RequestID, IdentityID: identity.IdentityID,
				Groups: append([]string(nil), identity.Groups...), DeviceID: identity.DeviceID,
				SessionID: identity.SessionID, Generation: identity.SessionGeneration,
				Namespace: identity.Namespace, NetworkSpecHash: identity.NetworkSpecHash,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Any("/gateway", echo.WrapHandler(tunnelHandler))
	response, err := server.Client().Get(server.URL + "/traffic/v1/exchange/" + strings.Repeat("a", 36))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy traffic route status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
	return e2eTrafficGateway{
		tickets: tickets, httpClient: server.Client(), certificate: server.Certificate(),
	}
}

type e2eTrafficAllocator struct {
	response relaycontrol.AllocationResponse
}

func (allocator e2eTrafficAllocator) Allocate(relaycontrol.AllocationRequest) (relaycontrol.AllocationResponse, error) {
	return allocator.response, nil
}

type e2eTrafficControlClient struct {
	t           *testing.T
	relayID     string
	coordinator trafficcontrolapi.Coordinator
}

func (client *e2eTrafficControlClient) RelayID() string { return client.relayID }

func (client *e2eTrafficControlClient) DoJSON(
	ctx context.Context,
	method string,
	path string,
	input any,
	output any,
) error {
	var (
		response any
		apiError *controlplaneapi.Error
	)
	switch {
	case method == http.MethodPost && path == trafficcontrol.InternalPathPrefix+"/claim":
		request, ok := input.(trafficcontrol.ClaimRequest)
		if !ok {
			return errors.New("traffic claim request type is invalid")
		}
		response, apiError = client.coordinator.Claim(ctx, client.relayID, request)
	case method == http.MethodPost && path == trafficcontrol.InternalPathPrefix+"/prepare":
		request, ok := input.(trafficcontrol.PrepareRequest)
		if !ok {
			return errors.New("traffic prepare request type is invalid")
		}
		response, apiError = client.coordinator.Prepare(ctx, client.relayID, request)
	case method == http.MethodPut && path == trafficcontrol.InternalPathPrefix+"/heartbeat":
		request, ok := input.(trafficcontrol.HeartbeatRequest)
		if !ok {
			return errors.New("traffic heartbeat request type is invalid")
		}
		response, apiError = client.coordinator.Heartbeat(ctx, client.relayID, request)
	case method == http.MethodPost && path == trafficcontrol.InternalPathPrefix+"/finish":
		request, ok := input.(trafficcontrol.FinishRequest)
		if !ok {
			return errors.New("traffic finish request type is invalid")
		}
		client.t.Logf("traffic stream finished: mode=%s task=%s failed=%t reason=%q", request.Mode, request.TaskID, request.Failed, request.Reason)
		response, apiError = client.coordinator.Finish(ctx, client.relayID, request)
	default:
		return fmt.Errorf("unsupported traffic control request %s %s", method, path)
	}
	if apiError != nil {
		return e2eTrafficControlError{apiError: apiError}
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}

type e2eTrafficControlError struct {
	apiError *controlplaneapi.Error
}

func (failure e2eTrafficControlError) Error() string { return failure.apiError.Message }

func (failure e2eTrafficControlError) HTTPStatus() int {
	switch failure.apiError.Code {
	case controlplaneapi.CodeUnauthenticated:
		return http.StatusUnauthorized
	case controlplaneapi.CodeForbidden:
		return http.StatusForbidden
	case controlplaneapi.CodeNotFound:
		return http.StatusNotFound
	case controlplaneapi.CodeConflict:
		return http.StatusConflict
	case controlplaneapi.CodeInvalidArgument:
		return http.StatusBadRequest
	case controlplaneapi.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
