package intercept

import (
	"context"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
)

type blockingGetCluster struct {
	*fakeCluster
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingGetCluster) GetService(
	ctx context.Context,
	contextName, namespace, service string,
) (*Service, error) {
	b.once.Do(func() {
		close(b.started)
		<-b.release
	})
	return b.fakeCluster.GetService(ctx, contextName, namespace, service)
}

type blockingApplyCluster struct {
	*fakeCluster
	started chan struct{}
	release chan struct{}
}

func (b *blockingApplyCluster) ApplyServiceIntercept(
	ctx context.Context,
	contextName string,
	request ServiceInterceptRequest,
) (Lease, []Backend, error) {
	lease, backends, err := b.fakeCluster.ApplyServiceIntercept(ctx, contextName, request)
	if err != nil {
		return nil, nil, err
	}
	close(b.started)
	<-b.release
	return lease, backends, nil
}

func TestConcurrentStartsReserveServiceKey(t *testing.T) {
	manager, listener := newConcurrencyTestManager(t, &blockingGetCluster{
		fakeCluster: &fakeCluster{service: concurrencyTestService()},
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	})
	defer listener.Close()
	defer func() { _ = manager.StopAll(context.Background()) }()
	clusterAPI := manager.cluster.(*blockingGetCluster)

	firstResult := make(chan error, 1)
	go func() {
		_, err := manager.StartIntercept(context.Background(), concurrencyTestMapping())
		firstResult <- err
	}()
	<-clusterAPI.started

	secondResult := make(chan error, 1)
	go func() {
		_, err := manager.StartMirror(context.Background(), concurrencyTestMapping())
		secondResult <- err
	}()
	select {
	case err := <-secondResult:
		if err == nil || !strings.Contains(err.Error(), "start is already in progress") {
			t.Fatalf("second start error = %v", err)
		}
	case <-time.After(3 * time.Second):
		close(clusterAPI.release)
		t.Fatal("concurrent start was not rejected before cluster access")
	}

	close(clusterAPI.release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first start: %v", err)
	}
	if len(manager.List()) != 1 {
		t.Fatalf("running exchanges = %d, want 1", len(manager.List()))
	}
}

func TestStartCrossingStopAllRollsBackBeforePublish(t *testing.T) {
	clusterAPI := &blockingApplyCluster{
		fakeCluster: &fakeCluster{service: concurrencyTestService()},
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	manager, listener := newConcurrencyTestManager(t, clusterAPI)
	defer listener.Close()

	result := make(chan error, 1)
	go func() {
		_, err := manager.StartIntercept(context.Background(), concurrencyTestMapping())
		result <- err
	}()
	<-clusterAPI.started

	if err := manager.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	close(clusterAPI.release)
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "session changed while starting") {
		t.Fatalf("start error = %v", err)
	}
	if !clusterAPI.restored {
		t.Fatal("intercept mutation was not compensated")
	}
	if len(manager.List()) != 0 {
		t.Fatalf("runtime was published after StopAll: %#v", manager.List())
	}
}

func newConcurrencyTestManager(t *testing.T, api ClusterAPI) (*Manager, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	manager := NewManager(api)
	if err := manager.Start(
		context.Background(),
		"minikube",
		"10.244.0.8",
		listener.Addr().String(),
	); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	return manager, listener
}

func concurrencyTestService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.1.10",
			Selector:  map[string]string{"app": "api"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
			}},
		},
	}
}

func concurrencyTestMapping() Mapping {
	return Mapping{
		Namespace: "default",
		Service:   "api",
		Ports: []PortMapping{{
			ServicePort: 80,
			Protocol:    "TCP",
			LocalHost:   "127.0.0.1",
			LocalPort:   18080,
		}},
	}
}
