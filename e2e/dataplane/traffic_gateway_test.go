//go:build e2e

package dataplane

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	ticketservice "github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficcontrolapi"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
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
	tickets    *ticketservice.Service
	httpClient *http.Client
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
		GatewayIP: gatewayIP, VerifyRequest: requestVerifier.Verify, ControlPlane: control,
		MirrorPrimaryDialContext: mirrorDial,
	})
	if err != nil {
		t.Fatal(err)
	}
	api.RegisterRoutes(router)
	return e2eTrafficGateway{tickets: tickets, httpClient: server.Client()}
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
