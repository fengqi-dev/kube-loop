package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeTestServer struct {
	serveStarted chan struct{}
	shutdown     chan struct{}
	shutdownOnce sync.Once
	serveErr     error
	shutdownErr  error
	shutdowns    atomic.Int32
}

func newRuntimeTestServer() *runtimeTestServer {
	return &runtimeTestServer{serveStarted: make(chan struct{}), shutdown: make(chan struct{})}
}

func (*runtimeTestServer) ListenAddress() string { return "127.0.0.1:0" }

func (server *runtimeTestServer) Serve(listener net.Listener) error {
	defer func() { _ = listener.Close() }()
	close(server.serveStarted)
	if server.serveErr != nil {
		return server.serveErr
	}
	<-server.shutdown
	return nil
}

func (server *runtimeTestServer) Shutdown(context.Context) error {
	server.shutdowns.Add(1)
	server.shutdownOnce.Do(func() { close(server.shutdown) })
	return server.shutdownErr
}

type runtimeTestWorker struct {
	started chan struct{}
	stopped chan struct{}
}

func newRuntimeTestWorker() *runtimeTestWorker {
	return &runtimeTestWorker{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (worker *runtimeTestWorker) Run(ctx context.Context) {
	close(worker.started)
	<-ctx.Done()
	close(worker.stopped)
}

type runtimeTestSessionRuntime struct {
	err       error
	shutdowns atomic.Int32
}

func (runtime *runtimeTestSessionRuntime) Shutdown(context.Context) error {
	runtime.shutdowns.Add(1)
	return runtime.err
}

func runtimeTestOptions(
	ctx context.Context,
	stop context.CancelFunc,
	server controlPlaneRuntimeServer,
	sessionRuntime controlPlaneSessionRuntime,
	workers ...controlPlaneWorker,
) serverRuntimeOptions {
	return serverRuntimeOptions{
		Context: ctx,
		Stop:    stop,
		Config: loadedControlPlaneConfig{
			Document:        controlPlaneConfigDocument{API: apiConfig{PublicURL: "https://control.example.test"}},
			ShutdownTimeout: time.Second,
		},
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Server:            server,
		SessionRecovery:   workers[0],
		MaintenanceWorker: workers[1],
		BindingRecovery:   workers[2],
		SessionRuntime:    sessionRuntime,
	}
}

func TestServeControlPlanePropagatesServeAndShutdownErrors(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	serveFailure := errors.New("serve control plane")
	serverShutdownFailure := errors.New("shutdown control plane")
	sessionShutdownFailure := errors.New("shutdown Sessions")
	server := newRuntimeTestServer()
	server.serveErr = serveFailure
	server.shutdownErr = serverShutdownFailure
	sessionRuntime := &runtimeTestSessionRuntime{err: sessionShutdownFailure}
	workers := []*runtimeTestWorker{
		newRuntimeTestWorker(), newRuntimeTestWorker(), newRuntimeTestWorker(),
	}

	err := serveControlPlane(runtimeTestOptions(
		ctx, stop, server, sessionRuntime, workers[0], workers[1], workers[2],
	))
	for _, expected := range []error{serveFailure, serverShutdownFailure, sessionShutdownFailure} {
		if !errors.Is(err, expected) {
			t.Fatalf("serveControlPlane() error = %v, want %v", err, expected)
		}
	}
	if ctx.Err() == nil || server.shutdowns.Load() != 1 || sessionRuntime.shutdowns.Load() != 1 {
		t.Fatalf(
			"shutdown state: context=%v server=%d Sessions=%d",
			ctx.Err(), server.shutdowns.Load(), sessionRuntime.shutdowns.Load(),
		)
	}
	for _, worker := range workers {
		select {
		case <-worker.stopped:
		default:
			t.Fatal("background worker remained active after serve failure")
		}
	}
}

func TestServeControlPlaneStopsCleanlyAfterContextCancellation(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	server := newRuntimeTestServer()
	sessionRuntime := &runtimeTestSessionRuntime{}
	workers := []*runtimeTestWorker{
		newRuntimeTestWorker(), newRuntimeTestWorker(), newRuntimeTestWorker(),
	}
	result := make(chan error, 1)
	go func() {
		result <- serveControlPlane(runtimeTestOptions(
			ctx, stop, server, sessionRuntime, workers[0], workers[1], workers[2],
		))
	}()

	<-server.serveStarted
	for _, worker := range workers {
		<-worker.started
	}
	stop()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveControlPlane did not stop after context cancellation")
	}
	if server.shutdowns.Load() != 1 || sessionRuntime.shutdowns.Load() != 1 {
		t.Fatalf(
			"shutdown calls: server=%d Sessions=%d",
			server.shutdowns.Load(), sessionRuntime.shutdowns.Load(),
		)
	}
}
