package trafficbindingclient

import (
	"context"
	"errors"
	"testing"
	"time"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSessionSynchronizerListsEveryIdentityBindingAcrossSessions(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	bindings := []struct {
		identityID string
		sessionID  string
		taskID     string
		namespace  string
	}{
		{
			identityID: "identity-a", sessionID: "11111111-1111-4111-8111-111111111111",
			taskID: "22222222-2222-4222-8222-222222222222", namespace: "development",
		},
		{
			identityID: "identity-a", sessionID: "33333333-3333-4333-8333-333333333333",
			taskID: "44444444-4444-4444-8444-444444444444", namespace: "development",
		},
		{
			identityID: "identity-b", sessionID: "55555555-5555-4555-8555-555555555555",
			taskID: "66666666-6666-4666-8666-666666666666", namespace: "development",
		},
		{
			identityID: "identity-a", sessionID: "77777777-7777-4777-8777-777777777777",
			taskID: "88888888-8888-4888-8888-888888888888", namespace: "production",
		},
	}
	for _, item := range bindings {
		binding := NewPendingPreviewBinding(
			Owner{
				IdentityID: item.identityID, SessionID: item.sessionID,
				TaskID: item.taskID, SessionGeneration: 1,
			},
			item.namespace, "preview-"+item.taskID[:8],
			[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}}, nil,
		)
		if _, _, err := manager.EnsureSession(context.Background(), binding); err != nil {
			t.Fatal(err)
		}
	}

	synchronizer, err := NewSessionSynchronizer(manager)
	if err != nil {
		t.Fatal(err)
	}
	items, err := synchronizer.List(context.Background(), "development", "identity-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != bindings[0].taskID || items[1].ID != bindings[1].taskID {
		t.Fatalf("List() = %#v", items)
	}
}

func TestSessionSynchronizerListsInterceptClusterIPFromSpec(t *testing.T) {
	t.Parallel()
	for _, mode := range []trafficv1alpha1.TrafficBindingMode{
		trafficv1alpha1.TrafficBindingModeExchange,
		trafficv1alpha1.TrafficBindingModeMirror,
	} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			manager := newFakeManager(t)
			binding, err := NewPendingInterceptBinding(
				mode,
				Owner{
					IdentityID:        "identity-a",
					SessionID:         "11111111-1111-4111-8111-111111111111",
					TaskID:            "22222222-2222-4222-8222-222222222222",
					SessionGeneration: 1,
				},
				"development",
				"api",
				"10.96.0.42",
				[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := manager.EnsureSession(context.Background(), binding); err != nil {
				t.Fatal(err)
			}
			synchronizer, err := NewSessionSynchronizer(manager)
			if err != nil {
				t.Fatal(err)
			}
			items, err := synchronizer.List(context.Background(), "development", "identity-a")
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ClusterIP != "10.96.0.42" {
				t.Fatalf("List() = %#v", items)
			}
		})
	}
}

func TestSessionSynchronizerDeletesOnlyUserOwnedBinding(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	binding := NewPendingPreviewBinding(
		Owner{
			IdentityID:        "identity-a",
			SessionID:         "11111111-1111-4111-8111-111111111111",
			TaskID:            "22222222-2222-4222-8222-222222222222",
			SessionGeneration: 1,
		},
		"development",
		"preview",
		[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
		nil,
	)
	if _, _, err := manager.EnsureSession(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	synchronizer, err := NewSessionSynchronizer(manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := synchronizer.Delete(
		context.Background(), binding.Namespace, "identity-b", binding.Spec.TaskID,
	); !errors.Is(err, ErrTrafficBindingNotFound) {
		t.Fatalf("foreign Delete() error = %v", err)
	}
	if _, err := manager.GetSession(
		context.Background(), binding.Namespace, binding.Spec.TaskID,
	); err != nil {
		t.Fatalf("foreign Delete() removed binding: %v", err)
	}
	if err := synchronizer.Delete(
		context.Background(), binding.Namespace, "identity-a", binding.Spec.TaskID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetSession(
		context.Background(), binding.Namespace, binding.Spec.TaskID,
	); !errors.Is(err, ErrTrafficBindingNotFound) {
		t.Fatalf("GetSession() after Delete = %v", err)
	}
}

func TestSessionSynchronizerBackfillsUserIDLabel(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	binding := NewPendingPreviewBinding(
		Owner{
			IdentityID:        "identity-a",
			SessionID:         "11111111-1111-4111-8111-111111111111",
			TaskID:            "22222222-2222-4222-8222-222222222222",
			SessionGeneration: 1,
		},
		"development",
		"preview",
		[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
		nil,
	)
	stored, _, err := manager.EnsureSession(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	before := stored.DeepCopy()
	delete(stored.Labels, userIDLabel)
	if err := manager.client.Patch(
		context.Background(),
		stored,
		client.MergeFrom(before),
	); err != nil {
		t.Fatal(err)
	}

	synchronizer, err := NewSessionSynchronizer(manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := synchronizer.Synchronize(
		context.Background(),
		"identity-a",
		binding.Spec.SessionID,
		binding.Namespace,
		1,
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	stored, err = manager.GetSession(
		context.Background(),
		binding.Namespace,
		binding.Spec.TaskID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Labels[userIDLabel]; got != "identity-a" {
		t.Fatalf("user ID label = %q", got)
	}
}

func TestSessionSynchronizerAdoptsActiveBindingWithoutChangingDesiredState(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	binding := NewPendingPreviewBinding(
		Owner{
			IdentityID:        "identity-a",
			SessionID:         "11111111-1111-4111-8111-111111111111",
			TaskID:            "22222222-2222-4222-8222-222222222222",
			SessionGeneration: 1,
		},
		"development",
		"preview",
		[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
		nil,
	)
	binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	if _, _, err := manager.EnsureSession(context.Background(), binding); err != nil {
		t.Fatal(err)
	}

	synchronizer, err := NewSessionSynchronizer(manager)
	if err != nil {
		t.Fatal(err)
	}
	const nextSessionID = "33333333-3333-4333-8333-333333333333"
	if err := synchronizer.Synchronize(
		context.Background(), "identity-a", nextSessionID, binding.Namespace, 2, time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.GetSession(context.Background(), binding.Namespace, binding.Spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStateActive {
		t.Fatalf("desired state = %q", stored.Spec.DesiredState)
	}
	if stored.Spec.SessionID != nextSessionID || stored.Spec.SessionGeneration != 2 {
		t.Fatalf(
			"adopted Session = %q/%d",
			stored.Spec.SessionID,
			stored.Spec.SessionGeneration,
		)
	}
}

func TestSessionSynchronizerRefreshesGenerationWithoutChangingDesiredState(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	binding := NewPendingPreviewBinding(
		Owner{
			IdentityID:        "identity-a",
			SessionID:         "11111111-1111-4111-8111-111111111111",
			TaskID:            "22222222-2222-4222-8222-222222222222",
			SessionGeneration: 1,
		},
		"development",
		"preview",
		[]entity.Port{{Name: "http", ServicePort: 8080, Protocol: "tcp"}},
		nil,
	)
	binding.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
	if _, _, err := manager.EnsureSession(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	synchronizer, err := NewSessionSynchronizer(manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := synchronizer.Synchronize(
		context.Background(),
		"identity-a",
		binding.Spec.SessionID,
		binding.Namespace,
		2,
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.GetSession(context.Background(), binding.Namespace, binding.Spec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.DesiredState != trafficv1alpha1.TrafficBindingDesiredStateActive {
		t.Fatalf("desired state = %q", stored.Spec.DesiredState)
	}
	if stored.Spec.SessionGeneration != 2 {
		t.Fatalf("generation = %d", stored.Spec.SessionGeneration)
	}
}
