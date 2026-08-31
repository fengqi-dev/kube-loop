package gateway

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type gatewayRuntimeTestGateway struct {
	beginDrain atomic.Int32
	drains     atomic.Int32
	drainErr   error
	drainValue any
}

type gatewayRuntimeContextKey struct{}

func (gateway *gatewayRuntimeTestGateway) BeginDrain() { gateway.beginDrain.Add(1) }

func (gateway *gatewayRuntimeTestGateway) Drain(ctx context.Context) error {
	gateway.drains.Add(1)
	gateway.drainValue = ctx.Value(gatewayRuntimeContextKey{})
	return gateway.drainErr
}

type gatewayRuntimeTestAdmissions struct{ beginDrain atomic.Int32 }

func (admissions *gatewayRuntimeTestAdmissions) BeginDrain() { admissions.beginDrain.Add(1) }

type gatewayRuntimeTestControl struct {
	drains     atomic.Int32
	drainErr   error
	drainValue any
}

type gatewayRuntimeTestAgent struct {
	done  chan struct{}
	stops atomic.Int32
}

func (agent *gatewayRuntimeTestAgent) Stop() { agent.stops.Add(1) }

func (agent *gatewayRuntimeTestAgent) Done() <-chan struct{} { return agent.done }

type gatewayRuntimeTestServe struct {
	started       chan struct{}
	waitForCancel bool
	release       <-chan struct{}
	err           error
	contextValue  any
}

func (serve *gatewayRuntimeTestServe) Serve(
	ctx context.Context,
	_ net.Listener,
	_ http.Handler,
) error {
	serve.contextValue = ctx.Value(gatewayRuntimeContextKey{})
	close(serve.started)
	if serve.waitForCancel {
		<-ctx.Done()
	}
	if serve.release != nil {
		<-serve.release
	}
	return serve.err
}

func (control *gatewayRuntimeTestControl) Drain(ctx context.Context) error {
	control.drains.Add(1)
	control.drainValue = ctx.Value(gatewayRuntimeContextKey{})
	return control.drainErr
}

func gatewayRuntimeTestOptions(
	ctx context.Context,
	listener net.Listener,
	serve gatewayServeFunc,
	gateway *gatewayRuntimeTestGateway,
	admissions *gatewayRuntimeTestAdmissions,
	control *gatewayRuntimeTestControl,
) gatewayRuntimeOptions {
	return gatewayRuntimeOptions{
		Context: ctx, Logger: log.New(io.Discard, "", 0),
		ListenAddress: listener.Addr().String(), Path: "/tunnel",
		Listener: listener, Handler: http.NotFoundHandler(),
		Gateway: gateway, Admissions: admissions, Control: control,
		DrainTimeout: 100 * time.Millisecond, ServeStopTimeout: time.Second, Serve: serve,
	}
}

func TestServeGatewayDrainsAndStopsAfterContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	parent := context.WithValue(t.Context(), gatewayRuntimeContextKey{}, "gateway-runtime")
	ctx, cancel := context.WithCancel(parent)
	serve := &gatewayRuntimeTestServe{started: make(chan struct{}), waitForCancel: true}
	gateway := &gatewayRuntimeTestGateway{}
	admissions := &gatewayRuntimeTestAdmissions{}
	control := &gatewayRuntimeTestControl{}
	result := make(chan error, 1)
	go func() {
		result <- serveGateway(gatewayRuntimeTestOptions(
			ctx, listener, serve.Serve, gateway, admissions, control,
		))
	}()

	<-serve.started
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveGateway did not stop after context cancellation")
	}
	if gateway.beginDrain.Load() != 1 || gateway.drains.Load() != 1 ||
		admissions.beginDrain.Load() != 1 || control.drains.Load() != 1 {
		t.Fatalf(
			"drain calls: Gateway begin=%d drain=%d admissions=%d control=%d",
			gateway.beginDrain.Load(), gateway.drains.Load(),
			admissions.beginDrain.Load(), control.drains.Load(),
		)
	}
	if serve.contextValue != "gateway-runtime" || gateway.drainValue != "gateway-runtime" ||
		control.drainValue != "gateway-runtime" {
		t.Fatalf(
			"runtime context values: Serve=%v Gateway=%v control=%v",
			serve.contextValue, gateway.drainValue, control.drainValue,
		)
	}
}

func TestServeGatewayPropagatesListenerFailureAfterDrain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serveFailure := errors.New("serve Gateway listener")
	serve := &gatewayRuntimeTestServe{started: make(chan struct{}), err: serveFailure}
	gateway := &gatewayRuntimeTestGateway{}
	admissions := &gatewayRuntimeTestAdmissions{}
	control := &gatewayRuntimeTestControl{}
	err = serveGateway(gatewayRuntimeTestOptions(
		t.Context(),
		listener,
		serve.Serve,
		gateway,
		admissions,
		control,
	))
	if !errors.Is(err, serveFailure) {
		t.Fatalf("serveGateway() error = %v, want %v", err, serveFailure)
	}
	if gateway.beginDrain.Load() != 1 || gateway.drains.Load() != 1 ||
		admissions.beginDrain.Load() != 1 || control.drains.Load() != 1 {
		t.Fatal("listener failure skipped Gateway drain lifecycle")
	}
}

func TestServeGatewayBoundsListenerStopWait(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	serve := &gatewayRuntimeTestServe{started: make(chan struct{}), release: release}
	gateway := &gatewayRuntimeTestGateway{}
	admissions := &gatewayRuntimeTestAdmissions{}
	control := &gatewayRuntimeTestControl{}
	options := gatewayRuntimeTestOptions(ctx, listener, serve.Serve, gateway, admissions, control)
	options.ServeStopTimeout = 50 * time.Millisecond
	result := make(chan error, 1)
	go func() { result <- serveGateway(options) }()

	<-serve.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("serveGateway() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("serveGateway ignored the listener-stop deadline")
	}
}

func TestStopGatewayAgentWaitsForDoneAndHonorsDeadline(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		agent := &gatewayRuntimeTestAgent{done: make(chan struct{})}
		close(agent.done)
		if err := stopGatewayAgent(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
		if agent.stops.Load() != 1 {
			t.Fatalf("Stop() calls = %d, want 1", agent.stops.Load())
		}
	})

	t.Run("deadline", func(t *testing.T) {
		agent := &gatewayRuntimeTestAgent{done: make(chan struct{})}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		if err := stopGatewayAgent(ctx, agent); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stopGatewayAgent() error = %v, want context deadline exceeded", err)
		}
		if agent.stops.Load() != 1 {
			t.Fatalf("Stop() calls = %d, want 1", agent.stops.Load())
		}
	})
}
