package trafficbindingclient

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
)

func TestTrafficSessionLifecycleUsesOnlyTrafficBinding(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	taskID := "22222222-2222-4222-8222-222222222222"
	binding, err := NewPendingInterceptBinding(
		trafficv1alpha1.TrafficBindingModeExchange,
		Owner{
			IdentityID: "identity-a", SessionID: "11111111-1111-4111-8111-111111111111",
			TaskID: taskID, SessionGeneration: 1,
		},
		"development", "api", "10.96.0.20",
		[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
		[]entity.LocalTarget{{ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 18080}},
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, created, err := manager.EnsureSession(context.Background(), binding)
	if err != nil || !created {
		t.Fatalf("EnsureSession() = created %v, error %v", created, err)
	}
	if stored.Spec.IdentityID != "identity-a" || stored.Spec.ClusterIP != "10.96.0.20" ||
		stored.Spec.Ports[0].LocalPort == nil || *stored.Spec.Ports[0].LocalPort != 18080 {
		t.Fatalf("stored TrafficBinding lost durable Session fields: %#v", stored.Spec)
	}
	stored, err = manager.ClaimRelay(context.Background(), stored, "relay-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AttachRelay(context.Background(), stored, "relay-a", "10.244.0.9",
		map[string]int32{"TCP/8080": 32001}); err != nil {
		t.Fatal(err)
	}
	stored, err = manager.GetSession(context.Background(), "development", taskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.Relay == nil || stored.Spec.Relay.Address != "10.244.0.9" ||
		stored.Spec.Ports[0].RelayPort == nil || *stored.Spec.Ports[0].RelayPort != 32001 {
		t.Fatalf("relay assignment was not persisted in TrafficBinding: %#v", stored.Spec)
	}
	if err := manager.ResetRelay(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	stored, err = manager.GetSession(context.Background(), "development", taskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.Relay != nil || stored.Spec.Ports[0].RelayPort != nil ||
		stored.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStateActive {
		t.Fatalf("reset did not return Session to pending relay state: %#v", stored.Spec)
	}
}

func TestSessionSynchronizerAdoptsPausedTrafficBindingWithoutTaskStore(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	binding := NewPendingPreviewBinding(
		Owner{
			IdentityID: "identity-a", SessionID: "11111111-1111-4111-8111-111111111111",
			TaskID: "22222222-2222-4222-8222-222222222222", SessionGeneration: 1,
		},
		"development", "preview-api",
		[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}}, nil,
	)
	stored, _, err := manager.EnsureSession(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	before := stored.DeepCopy()
	stored.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
	if err := manager.client.Patch(context.Background(), stored, client.MergeFrom(before)); err != nil {
		t.Fatal(err)
	}
	stored, _ = manager.GetSession(context.Background(), stored.Namespace, stored.Spec.TaskID)
	stored.Status.Phase = trafficv1alpha1.TrafficBindingPhasePaused
	stored.Status.ObservedGeneration = stored.Generation
	stored.Status.Conditions = []metav1.Condition{{
		Type: trafficv1alpha1.ConditionPaused, Status: metav1.ConditionTrue,
		ObservedGeneration: stored.Generation, LastTransitionTime: metav1.Now(),
	}}
	if err := manager.client.Status().Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	synchronizer, err := NewSessionSynchronizer(manager)
	if err != nil {
		t.Fatal(err)
	}
	newSessionID := "33333333-3333-4333-8333-333333333333"
	if err := synchronizer.Synchronize(
		context.Background(), "identity-a", newSessionID, "development", 2, time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	stored, err = manager.GetSession(context.Background(), "development", stored.Spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.SessionID != newSessionID || stored.Spec.SessionGeneration != 2 ||
		stored.Labels[sessionIDLabel] != newSessionID {
		t.Fatalf("TrafficBinding was not adopted: %#v", stored.Spec)
	}
}

func newFakeManager(t *testing.T) *Manager {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := trafficv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&trafficv1alpha1.TrafficBinding{}).Build()
	manager, err := New(kubernetesClient, Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
