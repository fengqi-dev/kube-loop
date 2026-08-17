package trafficapi_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

type fakeController struct {
	taskID   string
	prepared chan trafficcontrol.PrepareRequest
	finished chan trafficcontrol.FinishRequest
}

type writeCountingListener struct {
	net.Listener
	written atomic.Int64
}

func (listener *writeCountingListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &writeCountingConnection{Conn: connection, written: &listener.written}, nil
}

type writeCountingConnection struct {
	net.Conn
	written *atomic.Int64
}

func (connection *writeCountingConnection) Write(contents []byte) (int, error) {
	count, err := connection.Conn.Write(contents)
	connection.written.Add(int64(count))
	return count, err
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
	}
	return nil
}

func TestExchangeWebSocketRunsOnGatewayAndReportsLifecycle(t *testing.T) {
	taskID := uuid.NewString()
	controlPlane := &fakeController{
		taskID: taskID, prepared: make(chan trafficcontrol.PrepareRequest, 1),
		finished: make(chan trafficcontrol.FinishRequest, 1),
	}
	api, err := trafficapi.New(trafficapi.Config{
		GatewayIP: "127.0.0.1", ControlPlane: controlPlane, HeartbeatEvery: 20 * time.Millisecond,
		VerifyRequest: func(request *http.Request) (relayticket.Claims, error) {
			return relayticket.Claims{
				IdentityID: "user-1", Groups: []string{"developers"}, DeviceID: "device-1",
				SessionID: uuid.NewString(), SessionGeneration: 1, Namespace: "development",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := echo.New()
	api.RegisterRoutes(router)
	server := httptest.NewUnstartedServer(router)
	listener := &writeCountingListener{Listener: server.Listener}
	server.Listener = listener
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + trafficcontrol.PublicPathPrefix + "/exchange/" + taskID
	connection, _, err := websocket.Dial(context.Background(), endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	_, encoded, err := connection.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := exchangestream.Decode(encoded)
	if err != nil || frame.Type != exchangestream.Ready {
		t.Fatalf("ready frame = %#v, err = %v", frame, err)
	}
	writtenAfterReady := listener.written.Load()
	readContext, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()
	readDone := make(chan error, 1)
	go func() {
		_, _, readErr := connection.Read(readContext)
		readDone <- readErr
	}()
	deadline := time.Now().Add(time.Second)
	for listener.written.Load() == writtenAfterReady && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if listener.written.Load() == writtenAfterReady {
		t.Fatal("traffic WebSocket heartbeat did not write a keepalive frame")
	}
	stop, _ := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	if err := connection.Write(context.Background(), websocket.MessageBinary, stop); err != nil {
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
