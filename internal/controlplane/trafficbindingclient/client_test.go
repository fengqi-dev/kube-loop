package trafficbindingclient

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type taskReader map[string]controlplanestorage.Task

func (reader taskReader) GetByID(
	_ context.Context,
	id string,
) (controlplanestorage.Task, error) {
	task, ok := reader[id]
	if !ok {
		return controlplanestorage.Task{}, controlplanestorage.ErrNotFound
	}
	return task, nil
}

func (reader taskReader) ListStaleByTypeStates(
	_ context.Context,
	taskType string,
	states []remotetask.State,
	before time.Time,
	limit int,
) ([]controlplanestorage.Task, error) {
	items := make([]controlplanestorage.Task, 0)
	for _, task := range reader {
		if task.Type != taskType || !task.UpdatedAt.Before(before) {
			continue
		}
		if slices.Contains(states, task.State) {
			items = append(items, task)
		}
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

func (reader taskReader) ClaimStale(
	_ context.Context,
	id string,
	expected remotetask.State,
	observed time.Time,
	next remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	task, ok := reader[id]
	if !ok {
		return controlplanestorage.ErrNotFound
	}
	if task.State != expected || !task.UpdatedAt.Equal(observed) {
		return controlplanestorage.ErrConflict
	}
	task.State, task.Result, task.UpdatedAt = next, result, updatedAt
	reader[id] = task
	return nil
}

func (reader taskReader) UpdateState(
	_ context.Context,
	id string,
	expected, next remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	task, ok := reader[id]
	if !ok {
		return controlplanestorage.ErrNotFound
	}
	if task.State != expected {
		return controlplanestorage.ErrConflict
	}
	task.State, task.Result, task.UpdatedAt = next, result, updatedAt
	reader[id] = task
	return nil
}

type sessionReader map[string]controlplanestorage.Session

func (reader sessionReader) GetByID(
	_ context.Context,
	id string,
) (controlplanestorage.Session, error) {
	session, ok := reader[id]
	if !ok {
		return controlplanestorage.Session{}, controlplanestorage.ErrNotFound
	}
	return session, nil
}

func TestActivateWaitsForReadyAndIsIdempotent(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(
		kubernetesClient,
		Config{
			PollInterval:   10 * time.Millisecond,
			ControlPlaneID: "control-plane-a",
		},
	)
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
	if ready.Name != name || ready.Labels[taskIDLabel] != binding.Spec.TaskID ||
		ready.Labels[sessionIDLabel] != binding.Spec.SessionID ||
		ready.Labels[controlPlaneIDLabel] != manager.controlPlaneID {
		t.Fatalf("unexpected ready binding: %#v", ready.ObjectMeta)
	}
	if _, managed, err := manager.Activate(context.Background(), binding); err != nil ||
		!managed {
		t.Fatalf(
			"idempotent activation failed: managed=%v err=%v",
			managed,
			err,
		)
	}

	conflict := binding.DeepCopy()
	conflict.Spec.Ports[0].TargetPort++
	if _, managed, err := manager.Activate(context.Background(), conflict); err == nil ||
		managed {
		t.Fatalf(
			"conflicting activation should fail before ownership: managed=%v err=%v",
			managed,
			err,
		)
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
	manager, err := New(
		kubernetesClient,
		Config{PollInterval: 10 * time.Millisecond},
	)
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
				_ = kubernetesClient.Status().
					Update(context.Background(), current)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	_, managed, err := manager.Activate(context.Background(), binding)
	if err == nil || !managed ||
		!strings.Contains(err.Error(), "TargetNotFound") {
		t.Fatalf(
			"expected managed degraded error, got managed=%v err=%v",
			managed,
			err,
		)
	}
}

func TestPauseRetainsBindingAndActivateResumesIt(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(
		kubernetesClient,
		Config{PollInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding()
	binding.Name, _ = NameForTask(binding.Spec.TaskID)
	binding.Labels = map[string]string{
		managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
		taskIDLabel: binding.Spec.TaskID, sessionIDLabel: binding.Spec.SessionID,
	}
	desired := binding.DeepCopy()
	desired.Name = ""
	desired.Labels = nil
	if err := kubernetesClient.Create(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(binding)
	statusWhenState(t, kubernetesClient, key, trafficv1alpha1.TrafficBindingDesiredStatePaused,
		trafficv1alpha1.TrafficBindingPhasePaused, trafficv1alpha1.ConditionPaused)
	if err := manager.Pause(context.Background(), binding.Namespace, binding.Spec.TaskID); err != nil {
		t.Fatal(err)
	}
	retained := &trafficv1alpha1.TrafficBinding{}
	if err := kubernetesClient.Get(context.Background(), key, retained); err != nil {
		t.Fatalf("stopped binding was deleted: %v", err)
	}
	if retained.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		t.Fatalf("desired state = %q", retained.Spec.DesiredState)
	}

	statusWhenState(t, kubernetesClient, key, trafficv1alpha1.TrafficBindingDesiredStateActive,
		trafficv1alpha1.TrafficBindingPhaseReady, trafficv1alpha1.ConditionReady)
	active, managed, err := manager.Activate(context.Background(), desired)
	if err != nil || !managed {
		t.Fatalf("resume TrafficBinding: managed=%v err=%v", managed, err)
	}
	if active.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStateActive {
		t.Fatalf("resumed desired state = %q", active.Spec.DesiredState)
	}
	if err := manager.Delete(context.Background(), binding.Namespace, binding.Spec.TaskID); err != nil {
		t.Fatal(err)
	}
	if err := kubernetesClient.Get(
		context.Background(),
		key,
		&trafficv1alpha1.TrafficBinding{},
	); !apierrors.IsNotFound(err) {
		t.Fatalf("deleted binding get error = %v, want not found", err)
	}
}

func TestPausePortForwardReturnsAfterDesiredStatePatch(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(kubernetesClient, Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding()
	binding.Name, _ = NameForTask(binding.Spec.TaskID)
	binding.Labels = map[string]string{
		managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
		taskIDLabel: binding.Spec.TaskID, sessionIDLabel: binding.Spec.SessionID,
	}
	if err := kubernetesClient.Create(context.Background(), binding); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.Pause(ctx, binding.Namespace, binding.Spec.TaskID); err != nil {
		t.Fatalf("pause PortForward binding: %v", err)
	}
	paused := &trafficv1alpha1.TrafficBinding{}
	if err := kubernetesClient.Get(
		context.Background(), client.ObjectKeyFromObject(binding), paused,
	); err != nil {
		t.Fatal(err)
	}
	if paused.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		t.Fatalf("desired state = %q, want Paused", paused.Spec.DesiredState)
	}
}

func TestReconcilerDeletesOnlyOrphanedBindings(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(
		kubernetesClient,
		Config{PollInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	live := testBinding()
	orphan := testBinding()
	foreign := testBinding()
	stopped := testBinding()
	stopped.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
	for _, binding := range []*trafficv1alpha1.TrafficBinding{live, orphan, foreign, stopped} {
		binding.Name, _ = NameForTask(binding.Spec.TaskID)
		binding.Labels = map[string]string{
			managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
			taskIDLabel: binding.Spec.TaskID, sessionIDLabel: binding.Spec.SessionID,
		}
		if binding == foreign {
			binding.Labels[controlPlaneIDLabel] = "control-plane-b"
		}
		if err := kubernetesClient.Create(context.Background(), binding); err != nil {
			t.Fatal(err)
		}
	}
	tasks := taskReader{
		live.Spec.TaskID: {
			ID: live.Spec.TaskID, SessionID: live.Spec.SessionID, Type: "port-forward", State: remotetask.Running,
			UpdatedAt: time.Now(),
		},
		stopped.Spec.TaskID: {
			ID: stopped.Spec.TaskID, SessionID: stopped.Spec.SessionID,
			Type: "port-forward", State: remotetask.Stopped, UpdatedAt: time.Now(),
		},
	}
	reconciler, err := NewReconciler(manager, tasks, sessionReader{
		live.Spec.SessionID: {
			ID:        live.Spec.SessionID,
			Namespace: live.Namespace,
		},
	}, nil, ReconcilerConfig{Interval: time.Second, StaleAfter: 2 * time.Second, CleanupTimeout: time.Second, BatchSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := reconciler.RunOnce(context.Background())
	if err != nil || removed != 1 {
		t.Fatalf("reconcile orphans: removed=%d err=%v", removed, err)
	}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKeyFromObject(live),
		&trafficv1alpha1.TrafficBinding{},
	); err != nil {
		t.Fatalf("live binding was removed: %v", err)
	}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKeyFromObject(stopped),
		&trafficv1alpha1.TrafficBinding{},
	); err != nil {
		t.Fatalf("stopped binding was removed during startup recovery: %v", err)
	}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKeyFromObject(orphan),
		&trafficv1alpha1.TrafficBinding{},
	); err == nil {
		t.Fatal("orphaned binding still exists")
	}
	if err := kubernetesClient.Get(
		context.Background(),
		client.ObjectKeyFromObject(foreign),
		&trafficv1alpha1.TrafficBinding{},
	); err != nil {
		t.Fatalf("foreign Control Plane binding was removed: %v", err)
	}
}

func TestReconcilerClaimsStaleTaskAndLetsOperatorFinalizerOwnCleanup(
	t *testing.T,
) {
	kubernetesClient := fakeClient(t)
	manager, err := New(
		kubernetesClient,
		Config{PollInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := testBinding()
	binding.Spec.Mode = trafficv1alpha1.TrafficBindingModeExchange
	binding.Name, _ = NameForTask(binding.Spec.TaskID)
	binding.Labels = map[string]string{
		managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
		taskIDLabel: binding.Spec.TaskID, sessionIDLabel: binding.Spec.SessionID,
	}
	if err := kubernetesClient.Create(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tasks := taskReader{binding.Spec.TaskID: {
		ID: binding.Spec.TaskID, SessionID: binding.Spec.SessionID, Type: taskTypeExchange,
		State: remotetask.Running, UpdatedAt: now.Add(-time.Minute),
	}}
	reconciler, err := NewReconciler(manager, tasks, sessionReader{
		binding.Spec.SessionID: {
			ID:        binding.Spec.SessionID,
			Namespace: binding.Namespace,
		},
	}, nil, ReconcilerConfig{
		Interval: time.Second, StaleAfter: 2 * time.Second, CleanupTimeout: time.Second,
		BatchSize: 10, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := reconciler.RunOnce(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("recover stale Task: count=%d err=%v", count, err)
	}
	if task := tasks[binding.Spec.TaskID]; task.State != remotetask.Failed {
		t.Fatalf("stale Task state = %q", task.State)
	}
	getErr := kubernetesClient.Get(
		context.Background(),
		client.ObjectKeyFromObject(binding),
		&trafficv1alpha1.TrafficBinding{},
	)
	if !apierrors.IsNotFound(getErr) {
		t.Fatalf("stale TrafficBinding get error = %v, want not found", getErr)
	}
}

func TestControlPlaneIDProducesStableLabelValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "default", value: "", want: "kubeloop"},
		{name: "readable", value: " kubeloop-dev ", want: "kubeloop-dev"},
		{
			name:  "invalid",
			value: "control plane",
			want:  "sha256-a32b176c56bea1f4",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := controlPlaneID(test.value); got != test.want {
				t.Fatalf(
					"controlPlaneID(%q) = %q, want %q",
					test.value,
					got,
					test.want,
				)
			}
		})
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
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&trafficv1alpha1.TrafficBinding{}).
		Build()
}

func statusWhenState(
	t *testing.T,
	kubernetesClient client.Client,
	key client.ObjectKey,
	desiredState trafficv1alpha1.TrafficBindingDesiredState,
	phase trafficv1alpha1.TrafficBindingPhase,
	conditionType string,
) {
	t.Helper()
	go func() {
		for {
			current := &trafficv1alpha1.TrafficBinding{}
			if err := kubernetesClient.Get(context.Background(), key, current); err == nil &&
				current.Spec.DesiredState == desiredState {
				current.Status.ObservedGeneration = current.Generation
				current.Status.Phase = phase
				current.Status.Conditions = []metav1.Condition{{
					Type: conditionType, Status: metav1.ConditionTrue,
					Reason: string(phase), LastTransitionTime: metav1.Now(),
				}}
				_ = kubernetesClient.Status().Update(context.Background(), current)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
}

func testBinding() *trafficv1alpha1.TrafficBinding {
	return &trafficv1alpha1.TrafficBinding{
		Namespace: "development",
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         trafficv1alpha1.TrafficBindingModePortForward,
			SessionID:    uuid.NewString(), TaskID: uuid.NewString(), SessionGeneration: 1,
			Target: &trafficv1alpha1.TrafficTarget{
				Kind: trafficv1alpha1.TargetKindService,
				Name: "api",
			},
			Ports: []trafficv1alpha1.TrafficPort{{
				TargetPort: 8080, Protocol: trafficv1alpha1.TransportProtocolTCP,
			}},
		},
	}
}
