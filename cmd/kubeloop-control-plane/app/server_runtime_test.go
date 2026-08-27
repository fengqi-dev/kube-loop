package app

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

	"github.com/fengqi-dev/kube-loop/cmd/kubeloop-control-plane/app/options"
)

type runtimeTestServer struct {
	serveStarted          chan struct{}
	shutdown              chan struct{}
	shutdownOnce          sync.Once
	serveErr              error
	shutdownErr           error
	shutdownValue         any
	shutdownUntilDeadline bool
	shutdowns             atomic.Int32
}

type runtimeShutdownContextKey struct{}

type runtimeBlockingServer struct {
	serveStarted chan struct{}
	release      chan struct{}
	shutdowns    atomic.Int32
}

func (*runtimeBlockingServer) ListenAddress() string { return "127.0.0.1:0" }

func (server *runtimeBlockingServer) Serve(listener net.Listener) error {
	defer func() { _ = listener.Close() }()
	close(server.serveStarted)
	<-server.release
	return nil
}

func (server *runtimeBlockingServer) Shutdown(context.Context) error {
	server.shutdowns.Add(1)
	return nil
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

func (server *runtimeTestServer) Shutdown(ctx context.Context) error {
	server.shutdowns.Add(1)
	server.shutdownValue = ctx.Value(runtimeShutdownContextKey{})
	server.shutdownOnce.Do(func() { close(server.shutdown) })
	if server.shutdownUntilDeadline {
		<-ctx.Done()
		return ctx.Err()
	}
	return server.shutdownErr
}

type runtimeTestWorker struct {
	started chan struct{}
	stopped chan struct{}
}

type runtimeBlockingWorker struct {
	started chan struct{}
	release chan struct{}
}

func (worker *runtimeBlockingWorker) Run(context.Context) {
	close(worker.started)
	<-worker.release
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
	err           error
	entryErr      error
	shutdownValue any
	shutdowns     atomic.Int32
}

func (runtime *runtimeTestSessionRuntime) Shutdown(ctx context.Context) error {
	runtime.shutdowns.Add(1)
	runtime.entryErr = ctx.Err()
	runtime.shutdownValue = ctx.Value(runtimeShutdownContextKey{})
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
		Config: options.Config{
			Document:        options.Document{API: options.APIConfig{PublicURL: "https://control.example.test"}},
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
	parent := context.WithValue(t.Context(), runtimeShutdownContextKey{}, "runtime-shutdown")
	ctx, stop := context.WithCancel(parent)
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
	if server.shutdownValue != "runtime-shutdown" || sessionRuntime.shutdownValue != "runtime-shutdown" {
		t.Fatalf(
			"shutdown context values: server=%v Sessions=%v",
			server.shutdownValue,
			sessionRuntime.shutdownValue,
		)
	}
}

func TestServeControlPlaneBoundsBackgroundWorkerShutdown(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	server := newRuntimeTestServer()
	sessionRuntime := &runtimeTestSessionRuntime{}
	normalWorkers := []*runtimeTestWorker{newRuntimeTestWorker(), newRuntimeTestWorker()}
	blockingWorker := &runtimeBlockingWorker{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(blockingWorker.release) })
	options := runtimeTestOptions(
		ctx,
		stop,
		server,
		sessionRuntime,
		normalWorkers[0],
		normalWorkers[1],
		blockingWorker,
	)
	options.Config.ShutdownTimeout = 50 * time.Millisecond
	result := make(chan error, 1)
	go func() { result <- serveControlPlane(options) }()

	<-server.serveStarted
	for _, worker := range normalWorkers {
		<-worker.started
	}
	<-blockingWorker.started
	stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("serveControlPlane() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveControlPlane ignored the background worker shutdown deadline")
	}
}

func TestServeControlPlaneBoundsServeLoopShutdown(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	server := &runtimeBlockingServer{serveStarted: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(server.release) })
	sessionRuntime := &runtimeTestSessionRuntime{}
	workers := []*runtimeTestWorker{
		newRuntimeTestWorker(), newRuntimeTestWorker(), newRuntimeTestWorker(),
	}
	options := runtimeTestOptions(
		ctx, stop, server, sessionRuntime, workers[0], workers[1], workers[2],
	)
	options.Config.ShutdownTimeout = 50 * time.Millisecond
	result := make(chan error, 1)
	go func() { result <- serveControlPlane(options) }()

	<-server.serveStarted
	for _, worker := range workers {
		<-worker.started
	}
	stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("serveControlPlane() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("serveControlPlane ignored the serve-loop shutdown deadline")
	}
}

func TestServeControlPlaneStartsIndependentShutdownsConcurrently(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	server := newRuntimeTestServer()
	server.shutdownUntilDeadline = true
	sessionRuntime := &runtimeTestSessionRuntime{}
	workers := []*runtimeTestWorker{
		newRuntimeTestWorker(), newRuntimeTestWorker(), newRuntimeTestWorker(),
	}
	options := runtimeTestOptions(
		ctx, stop, server, sessionRuntime, workers[0], workers[1], workers[2],
	)
	options.Config.ShutdownTimeout = 50 * time.Millisecond
	result := make(chan error, 1)
	go func() { result <- serveControlPlane(options) }()

	<-server.serveStarted
	for _, worker := range workers {
		<-worker.started
	}
	stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("serveControlPlane() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveControlPlane did not honor the parallel shutdown deadline")
	}
	if sessionRuntime.shutdowns.Load() != 1 || sessionRuntime.entryErr != nil {
		t.Fatalf(
			"Session shutdown calls=%d entry context error=%v",
			sessionRuntime.shutdowns.Load(),
			sessionRuntime.entryErr,
		)
	}
}
