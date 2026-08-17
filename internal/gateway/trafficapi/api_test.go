package trafficapi_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/google/uuid"
)

type fakeController struct {
	prepared chan trafficcontrol.PrepareRequest
	finished chan trafficcontrol.FinishRequest
	claimErr error
}

func (controlPlane *fakeController) RelayID() string { return "relay-test" }

func (controlPlane *fakeController) DoJSON(
	_ context.Context,
	_ string,
	path string,
	input, output any,
) error {
	switch path {
	case trafficcontrol.InternalPathPrefix + "/claim":
		if controlPlane.claimErr != nil {
			return controlPlane.claimErr
		}
		request := input.(trafficcontrol.ClaimRequest)
		*output.(*trafficcontrol.ClaimResponse) = trafficcontrol.ClaimResponse{
			Mode: request.Mode, TaskID: request.TaskID, Service: "api",
			Ports: []trafficmodel.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
		}
	case trafficcontrol.InternalPathPrefix + "/prepare":
		request := input.(trafficcontrol.PrepareRequest)
		controlPlane.prepared <- request
		*output.(*trafficcontrol.PrepareResponse) = trafficcontrol.PrepareResponse{}
	case trafficcontrol.InternalPathPrefix + "/heartbeat":
		*output.(*trafficcontrol.HeartbeatResponse) = trafficcontrol.HeartbeatResponse{}
	case trafficcontrol.InternalPathPrefix + "/finish":
		request := input.(trafficcontrol.FinishRequest)
		controlPlane.finished <- request
		*output.(*trafficcontrol.FinishResponse) = trafficcontrol.FinishResponse{State: "stopped"}
	default:
		return errors.New("unexpected traffic control path")
	}
	return nil
}

func TestExchangeLogicalStreamRunsOnGatewayAndReportsLifecycle(t *testing.T) {
	taskID := uuid.NewString()
	controlPlane := &fakeController{
		prepared: make(chan trafficcontrol.PrepareRequest, 1),
		finished: make(chan trafficcontrol.FinishRequest, 1),
	}
	api, err := trafficapi.New(trafficapi.Config{
		GatewayIP: "127.0.0.1", ControlPlane: controlPlane, HeartbeatEvery: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	gatewayConnection, clientConnection := net.Pipe()
	t.Cleanup(func() { _ = clientConnection.Close() })
	identity := trafficcontrol.Identity{
		IdentityID: "user-1", Groups: []string{"developers"}, DeviceID: "device-1",
		SessionID: uuid.NewString(), SessionGeneration: 1, Namespace: "development",
	}
	go api.ServeTraffic(
		context.Background(),
		gatewayConnection,
		identity,
		tunnel.TrafficOpenRequest{Mode: tunnel.TrafficModeExchange, TaskID: taskID},
	)
	if err := tunnel.ReadStatus(clientConnection); err != nil {
		t.Fatal(err)
	}
	framed, err := trafficstream.Dial(context.Background(), clientConnection)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := framed.ReadFrame(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil || frame.Type != exchangestream.Ready {
		t.Fatalf("ready frame = %#v, err = %v", frame, err)
	}
	stop, err := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	if err != nil {
		t.Fatal(err)
	}
	if err := framed.WriteFrame(context.Background(), stop); err != nil {
		t.Fatal(err)
	}

	select {
	case prepared := <-controlPlane.prepared:
		if prepared.GatewayIP != "127.0.0.1" || len(prepared.Ports) != 1 || prepared.Ports[0].ListenPort == 0 {
			t.Fatalf("prepare = %#v", prepared)
		}
	case <-time.After(time.Second):
		t.Fatal("ControlPlane prepare was not called")
	}
	select {
	case finished := <-controlPlane.finished:
		if finished.Failed || finished.TaskID != taskID {
			t.Fatalf("finish = %#v", finished)
		}
	case <-time.After(time.Second):
		t.Fatal("ControlPlane finish was not called")
	}
}

func TestClaimFailureWritesTunnelStatusErrorBeforeTrafficFrames(t *testing.T) {
	controlPlane := &fakeController{
		prepared: make(chan trafficcontrol.PrepareRequest, 1),
		finished: make(chan trafficcontrol.FinishRequest, 1),
		claimErr: errors.New("claim failed"),
	}
	api, err := trafficapi.New(trafficapi.Config{GatewayIP: "127.0.0.1", ControlPlane: controlPlane})
	if err != nil {
		t.Fatal(err)
	}
	gatewayConnection, clientConnection := net.Pipe()
	t.Cleanup(func() { _ = clientConnection.Close() })
	go api.ServeTraffic(
		context.Background(),
		gatewayConnection,
		trafficcontrol.Identity{
			IdentityID: "user-1", DeviceID: "device-1", SessionID: uuid.NewString(),
			SessionGeneration: 1, Namespace: "development",
		},
		tunnel.TrafficOpenRequest{Mode: tunnel.TrafficModeExchange, TaskID: uuid.NewString()},
	)
	if err := tunnel.ReadStatus(clientConnection); err == nil {
		t.Fatal("claim failure unexpectedly returned a successful tunnel status")
	}
	_ = clientConnection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var extra [1]byte
	if _, err := clientConnection.Read(extra[:]); err == nil {
		t.Fatal("claim failure wrote traffic data after the status error")
	}
}

var _ interface {
	RelayID() string
	DoJSON(context.Context, string, string, any, any) error
} = (*fakeController)(nil)
