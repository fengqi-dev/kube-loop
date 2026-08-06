package inventory

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestWatchEmitsInformerSnapshot(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "service", Namespace: "default"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "deployment", Namespace: "default"}},
	)
	snapshots := make(chan Snapshot, 1)
	watcher, err := Watch(
		context.Background(),
		client,
		[]string{"default"},
		func(snapshot Snapshot) { snapshots <- snapshot },
	)
	if err != nil {
		t.Fatalf("watch inventory: %v", err)
	}
	defer watcher.Close()

	select {
	case snapshot := <-snapshots:
		if len(snapshot.Pods) != 1 ||
			len(snapshot.Services) != 1 ||
			len(snapshot.Deployments) != 1 {
			t.Fatalf("snapshot = %+v", snapshot)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for inventory snapshot")
	}
}

func TestWatchRejectsMissingDependencies(t *testing.T) {
	if _, err := Watch(context.Background(), nil, nil, func(Snapshot) {}); err == nil {
		t.Fatal("watch accepted a nil Kubernetes client")
	}
	client := fake.NewSimpleClientset()
	if _, err := Watch(context.Background(), client, nil, nil); err == nil {
		t.Fatal("watch accepted a nil callback")
	}
}
