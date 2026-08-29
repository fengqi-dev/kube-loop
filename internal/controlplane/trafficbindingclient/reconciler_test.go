package trafficbindingclient

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type recoveryContextKey struct{}

type recordingTaskReader struct {
	taskReader

	updateValue any
}

func (reader *recordingTaskReader) UpdateState(
	ctx context.Context,
	id string,
	expected, next remotetask.State,
	result json.RawMessage,
	updatedAt time.Time,
) error {
	reader.updateValue = ctx.Value(recoveryContextKey{})
	return reader.taskReader.UpdateState(ctx, id, expected, next, result, updatedAt)
}

func TestReconcilerDefersRecoveryWhenSessionIsUnavailable(t *testing.T) {
	manager, err := New(fakeClient(t), Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := controlplanestorage.Task{
		ID: "task-a", SessionID: "session-a", Type: taskTypeExchange,
		State: remotetask.Running, UpdatedAt: now.Add(-time.Minute),
	}
	tasks := &recordingTaskReader{taskReader: taskReader{task.ID: task}}
	reconciler, err := NewReconciler(
		manager,
		tasks,
		sessionReader{},
		nil,
		ReconcilerConfig{
			Interval: time.Second, StaleAfter: 2 * time.Second,
			CleanupTimeout: time.Second, BatchSize: 10, Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	parent, cancel := context.WithCancel(context.WithValue(t.Context(), recoveryContextKey{}, "recovery"))
	cancel()
	claimed, err := reconciler.recoverTask(parent, task, now)
	if !claimed || err == nil || !errors.Is(err, controlplanestorage.ErrNotFound) {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	deferred := tasks.taskReader[task.ID]
	if deferred.State != remotetask.Recovering || !deferred.UpdatedAt.Equal(now) {
		t.Fatalf("deferred task=%#v", deferred)
	}
	if tasks.updateValue != "recovery" {
		t.Fatalf("deferred recovery context value = %v", tasks.updateValue)
	}
}

func TestReconcilerSkipsConflictingStaleClaim(t *testing.T) {
	manager, err := New(fakeClient(t), Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := controlplanestorage.Task{
		ID: "task-a", SessionID: "session-a", Type: taskTypeExchange,
		State: remotetask.Running, UpdatedAt: now.Add(-time.Minute),
	}
	current := task
	current.State = remotetask.Stopping
	tasks := taskReader{task.ID: current}
	reconciler, err := NewReconciler(
		manager,
		tasks,
		sessionReader{},
		nil,
		ReconcilerConfig{
			Interval: time.Second, StaleAfter: 2 * time.Second,
			CleanupTimeout: time.Second, BatchSize: 10, Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := reconciler.recoverTask(t.Context(), task, now)
	if claimed || err != nil {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
}

func TestReconcilerCompletesPauseAfterStartup(t *testing.T) {
	kubernetesClient := fakeClient(t)
	manager, err := New(kubernetesClient, Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	binding := testBinding()
	binding.Spec.TaskID = uuid.NewString()
	binding.Name, _ = NameForTask(binding.Spec.TaskID)
	binding.Labels = map[string]string{
		managedByLabel: managedByValue, controlPlaneIDLabel: manager.controlPlaneID,
		taskIDLabel: binding.Spec.TaskID, sessionIDLabel: binding.Spec.SessionID,
	}
	if err := kubernetesClient.Create(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	statusWhenState(
		t,
		kubernetesClient,
		client.ObjectKeyFromObject(binding),
		trafficv1alpha1.TrafficBindingDesiredStatePaused,
		trafficv1alpha1.TrafficBindingPhasePaused,
		trafficv1alpha1.ConditionPaused,
	)
	task := controlplanestorage.Task{
		ID: binding.Spec.TaskID, SessionID: binding.Spec.SessionID,
		Type: "port-forward", State: remotetask.Stopping, UpdatedAt: now.Add(-time.Minute),
	}
	tasks := taskReader{task.ID: task}
	reconciler, err := NewReconciler(
		manager,
		tasks,
		sessionReader{task.SessionID: {
			ID: task.SessionID, Namespace: binding.Namespace,
		}},
		nil,
		ReconcilerConfig{
			Interval: time.Second, StaleAfter: 2 * time.Second,
			CleanupTimeout: time.Second, BatchSize: 10, Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := reconciler.recoverTask(t.Context(), task, now)
	if err != nil || !claimed {
		t.Fatalf("recover stopping task: claimed=%v err=%v", claimed, err)
	}
	if got := tasks[task.ID].State; got != remotetask.Stopped {
		t.Fatalf("recovered task state = %q", got)
	}
	retained := &trafficv1alpha1.TrafficBinding{}
	if err := kubernetesClient.Get(
		t.Context(),
		client.ObjectKeyFromObject(binding),
		retained,
	); err != nil {
		t.Fatalf("paused binding was not retained: %v", err)
	}
	if retained.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStatePaused {
		t.Fatalf("recovered desired state = %q", retained.Spec.DesiredState)
	}
}

func TestReconcilerRunStopsWithContext(t *testing.T) {
	manager, err := New(fakeClient(t), Config{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(
		manager,
		taskReader{},
		sessionReader{},
		nil,
		ReconcilerConfig{
			Interval: 100 * time.Millisecond, StaleAfter: 200 * time.Millisecond,
			CleanupTimeout: time.Second, BatchSize: 10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Reconciler.Run did not stop after cancellation")
	}
}

func TestTaskTypeForMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     trafficv1alpha1.TrafficBindingMode
		wantType string
		wantOK   bool
	}{
		{
			name: "port forward", mode: trafficv1alpha1.TrafficBindingModePortForward,
			wantType: "port-forward", wantOK: true,
		},
		{
			name: "preview", mode: trafficv1alpha1.TrafficBindingModePreview,
			wantType: "preview", wantOK: true,
		},
		{
			name: "exchange", mode: trafficv1alpha1.TrafficBindingModeExchange,
			wantType: taskTypeExchange, wantOK: true,
		},
		{
			name: "mirror", mode: trafficv1alpha1.TrafficBindingModeMirror,
			wantType: "mirror", wantOK: true,
		},
		{name: "unknown", mode: trafficv1alpha1.TrafficBindingMode("unknown")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotType, gotOK := taskTypeForMode(test.mode)
			if gotType != test.wantType || gotOK != test.wantOK {
				t.Fatalf("taskTypeForMode(%q) = %q, %v", test.mode, gotType, gotOK)
			}
		})
	}
}
