package intercept

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type blockingRestoreCluster struct {
	*fakeCluster
	started chan struct{}
	release chan struct{}
}

func (b *blockingRestoreCluster) ApplyServiceIntercept(
	ctx context.Context,
	contextName string,
	request ServiceInterceptRequest,
) (Lease, []Backend, error) {
	lease, backends, err := b.fakeCluster.ApplyServiceIntercept(ctx, contextName, request)
	if err != nil {
		return nil, nil, err
	}
	return fakeLease{release: func(ctx context.Context) error {
		close(b.started)
		<-b.release
		return lease.Release(ctx)
	}}, backends, nil
}

func TestStopRestoreFailureKeepsInterceptForRetry(t *testing.T) {
	clusterAPI := &fakeCluster{service: concurrencyTestService()}
	manager, listener := newConcurrencyTestManager(t, clusterAPI)
	defer listener.Close()
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartIntercept(context.Background(), concurrencyTestMapping())
	if err != nil {
		t.Fatal(err)
	}
	restoreErr := errors.New("restore unavailable")
	clusterAPI.restoreErr = restoreErr
	if err := manager.Stop(context.Background(), info.ID); !errors.Is(err, restoreErr) {
		t.Fatalf("Stop error = %v, want %v", err, restoreErr)
	}
	if len(manager.List()) != 1 {
		t.Fatalf("running exchanges = %d after failed stop, want 1", len(manager.List()))
	}
	if _, ok := manager.HostTCP("10.96.1.10", 80); !ok {
		t.Fatal("host route was removed after failed stop")
	}

	clusterAPI.restoreErr = nil
	if err := manager.Stop(context.Background(), info.ID); err != nil {
		t.Fatalf("retry Stop: %v", err)
	}
	if clusterAPI.restoreCalls != 2 || !clusterAPI.restored {
		t.Fatalf(
			"restore calls = %d, restored = %v",
			clusterAPI.restoreCalls,
			clusterAPI.restored,
		)
	}
	if len(manager.List()) != 0 {
		t.Fatalf("running exchanges = %d after retry, want 0", len(manager.List()))
	}
	if _, ok := manager.HostTCP("10.96.1.10", 80); ok {
		t.Fatal("host route remains after successful retry")
	}
}

func TestStopDeleteFailureKeepsPreviewForRetry(t *testing.T) {
	clusterAPI := &fakeCluster{previewIP: "10.96.9.9"}
	manager, listener := newConcurrencyTestManager(t, clusterAPI)
	defer listener.Close()
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartPreview(context.Background(), PreviewRequest{
		Namespace: "default",
		Name:      "preview",
		Ports: []PortMapping{{
			ServicePort: 8080,
			Protocol:    "TCP",
			LocalHost:   "127.0.0.1",
			LocalPort:   18080,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	deleteErr := errors.New("delete unavailable")
	clusterAPI.deleteErr = deleteErr
	if err := manager.Stop(context.Background(), info.ID); !errors.Is(err, deleteErr) {
		t.Fatalf("Stop error = %v, want %v", err, deleteErr)
	}
	if len(manager.ListPreviews()) != 1 {
		t.Fatalf(
			"running previews = %d after failed stop, want 1",
			len(manager.ListPreviews()),
		)
	}
	if _, ok := manager.HostTCP("10.96.9.9", 8080); !ok {
		t.Fatal("preview host route was removed after failed stop")
	}

	clusterAPI.deleteErr = nil
	if err := manager.Stop(context.Background(), info.ID); err != nil {
		t.Fatalf("retry Stop: %v", err)
	}
	if clusterAPI.deleteCalls != 2 || !clusterAPI.deleted {
		t.Fatalf(
			"delete calls = %d, deleted = %v",
			clusterAPI.deleteCalls,
			clusterAPI.deleted,
		)
	}
	if len(manager.ListPreviews()) != 0 {
		t.Fatalf(
			"running previews = %d after retry, want 0",
			len(manager.ListPreviews()),
		)
	}
	if _, ok := manager.HostTCP("10.96.9.9", 8080); ok {
		t.Fatal("preview host route remains after successful retry")
	}
}

func TestConcurrentStopDoesNotRestoreTwice(t *testing.T) {
	clusterAPI := &blockingRestoreCluster{
		fakeCluster: &fakeCluster{service: concurrencyTestService()},
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	manager, listener := newConcurrencyTestManager(t, clusterAPI)
	defer listener.Close()
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartIntercept(context.Background(), concurrencyTestMapping())
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- manager.Stop(context.Background(), info.ID)
	}()
	<-clusterAPI.started

	err = manager.Stop(context.Background(), info.ID)
	if err == nil || !strings.Contains(err.Error(), "stop already in progress") {
		t.Fatalf("concurrent Stop error = %v", err)
	}
	close(clusterAPI.release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if clusterAPI.restoreCalls != 1 {
		t.Fatalf("restore calls = %d, want 1", clusterAPI.restoreCalls)
	}
}
