package main

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
}

func (gateway *gatewayRuntimeTestGateway) BeginDrain() { gateway.beginDrain.Add(1) }

func (gateway *gatewayRuntimeTestGateway) Drain(context.Context) error {
	gateway.drains.Add(1)
	return gateway.drainErr
}

type gatewayRuntimeTestAdmissions struct{ beginDrain atomic.Int32 }

func (admissions *gatewayRuntimeTestAdmissions) BeginDrain() { admissions.beginDrain.Add(1) }

type gatewayRuntimeTestControl struct {
	drains   atomic.Int32
	drainErr error
}

type gatewayRuntimeTestServe struct {
	started       chan struct{}
	waitForCancel bool
	err           error
}

func (serve *gatewayRuntimeTestServe) Serve(
	ctx context.Context,
	_ net.Listener,
	_ http.Handler,
) error {
	close(serve.started)
	if serve.waitForCancel {
		<-ctx.Done()
	}
	return serve.err
}

func (control *gatewayRuntimeTestControl) Drain(context.Context) error {
	control.drains.Add(1)
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
		DrainTimeout: 100 * time.Millisecond, Serve: serve,
	}
}

func TestServeGatewayDrainsAndStopsAfterContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
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
