package trafficbindingclient

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controllerstorage "github.com/fengqi-dev/kube-loop/internal/controller/storage"
	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/internal/operator/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
)

type taskReader map[string]controllerstorage.Task

func (reader taskReader) GetByID(_ context.Context, id string) (controllerstorage.Task, error) {
	task, ok := reader[id]
	if !ok {
		return controllerstorage.Task{}, controllerstorage.ErrNotFound
	}
	return task, nil
}

func TestActivateWaitsForReadyAndIsIdempotent(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(kubernetesClient, Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding()
	name, _ := NameForTask(binding.Spec.TaskID)
	key := client.ObjectKey{Namespace: binding.Namespace, Name: name}
	statusDone := make(chan error, 1)
	go func() {
		for {
			current := &trafficv1alpha1.TrafficBinding{}
			if getErr := kubernetesClient.Get(context.Background(), key, current); getErr == nil {
				current.Status.ObservedGeneration = current.Generation
				current.Status.Phase = trafficv1alpha1.TrafficBindingPhaseReady
				current.Status.Conditions = []metav1.Condition{{
					Type: trafficv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
					Reason: "PortForwardReady", LastTransitionTime: metav1.Now(),
				}}
				statusDone <- kubernetesClient.Status().Update(context.Background(), current)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	ready, managed, err := manager.Activate(context.Background(), binding)
	if err != nil || !managed {
		t.Fatalf("activate TrafficBinding: managed=%v err=%v", managed, err)
	}
	if err := <-statusDone; err != nil {
		t.Fatal(err)
	}
	if ready.Name != name || ready.Labels[taskIDLabel] != binding.Spec.TaskID || ready.Labels[sessionIDLabel] != binding.Spec.SessionID {
		t.Fatalf("unexpected ready binding: %#v", ready.ObjectMeta)
	}
	if _, managed, err := manager.Activate(context.Background(), binding); err != nil || !managed {
		t.Fatalf("idempotent activation failed: managed=%v err=%v", managed, err)
	}

	conflict := binding.DeepCopy()
	conflict.Spec.Ports[0].TargetPort++
	if _, managed, err := manager.Activate(context.Background(), conflict); err == nil || managed {
		t.Fatalf("conflicting activation should fail before ownership: managed=%v err=%v", managed, err)
	}
	if err := manager.Delete(context.Background(), binding.Namespace, binding.Spec.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(context.Background(), binding.Namespace, binding.Spec.TaskID); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestActivateReturnsManagedDegradedError(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(kubernetesClient, Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding()
	name, _ := NameForTask(binding.Spec.TaskID)
	key := client.ObjectKey{Namespace: binding.Namespace, Name: name}
	go func() {
		for {
			current := &trafficv1alpha1.TrafficBinding{}
			if getErr := kubernetesClient.Get(context.Background(), key, current); getErr == nil {
				current.Status.ObservedGeneration = current.Generation
				current.Status.Phase = trafficv1alpha1.TrafficBindingPhaseDegraded
				current.Status.Conditions = []metav1.Condition{{
					Type: trafficv1alpha1.ConditionDegraded, Status: metav1.ConditionTrue,
					Reason: "TargetNotFound", Message: "target is unavailable", LastTransitionTime: metav1.Now(),
				}}
				_ = kubernetesClient.Status().Update(context.Background(), current)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	_, managed, err := manager.Activate(context.Background(), binding)
	if err == nil || !managed || !strings.Contains(err.Error(), "TargetNotFound") {
		t.Fatalf("expected managed degraded error, got managed=%v err=%v", managed, err)
	}
}

func TestReconcilerDeletesOnlyOrphanedBindings(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(kubernetesClient, Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	live := testBinding()
	orphan := testBinding()
	for _, binding := range []*trafficv1alpha1.TrafficBinding{live, orphan} {
		binding.Name, _ = NameForTask(binding.Spec.TaskID)
		binding.Labels = map[string]string{
			managedByLabel: managedByValue, taskIDLabel: binding.Spec.TaskID, sessionIDLabel: binding.Spec.SessionID,
		}
		if err := kubernetesClient.Create(context.Background(), binding); err != nil {
			t.Fatal(err)
		}
	}
	reconciler, err := NewReconciler(manager, taskReader{
		live.Spec.TaskID: {
			ID: live.Spec.TaskID, SessionID: live.Spec.SessionID, Type: "port-forward", State: remotetask.Running,
		},
	}, nil, ReconcilerConfig{Interval: time.Second, CleanupTimeout: time.Second, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := reconciler.RunOnce(context.Background())
	if err != nil || removed != 1 {
		t.Fatalf("reconcile orphans: removed=%d err=%v", removed, err)
	}
	if err := kubernetesClient.Get(context.Background(), client.ObjectKeyFromObject(live), &trafficv1alpha1.TrafficBinding{}); err != nil {
		t.Fatalf("live binding was removed: %v", err)
	}
	if err := kubernetesClient.Get(context.Background(), client.ObjectKeyFromObject(orphan), &trafficv1alpha1.TrafficBinding{}); err == nil {
		t.Fatal("orphaned binding still exists")
	}
}

func fakeClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := trafficv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&trafficv1alpha1.TrafficBinding{}).Build()
}

func testBinding() *trafficv1alpha1.TrafficBinding {
	return &trafficv1alpha1.TrafficBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: "development"},
		Spec: trafficv1alpha1.TrafficBindingSpec{
			Mode:      trafficv1alpha1.TrafficBindingModePortForward,
			SessionID: uuid.NewString(), TaskID: uuid.NewString(), SessionGeneration: 1,
			Target: &trafficv1alpha1.TrafficTarget{Kind: trafficv1alpha1.TargetKindService, Name: "api"},
			Ports: []trafficv1alpha1.TrafficPort{{
				TargetPort: 8080, Protocol: trafficv1alpha1.TransportProtocolTCP,
			}},
		},
	}
}
