package trafficbindingclient

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

func TestWaitForPausedReturnsPermanentPauseFailure(t *testing.T) {
	t.Parallel()
	manager := newFakeManager(t)
	key := types.NamespacedName{Namespace: "development", Name: "kubeloop-broken"}
	binding := &trafficv1alpha1.TrafficBinding{
		Name:      key.Name,
		Namespace: key.Namespace,
	}
	if err := manager.client.Create(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhasePausing
	binding.Status.Conditions = []metav1.Condition{{
		Type: trafficv1alpha1.ConditionDegraded, Status: metav1.ConditionTrue,
		Reason: "PauseFailed", Message: "rollback snapshot is missing",
		ObservedGeneration: binding.Generation, LastTransitionTime: metav1.Now(),
	}}
	if err := manager.client.Status().Patch(context.Background(), binding, client.MergeFrom(before)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	err := manager.waitForPaused(ctx, key)
	if err == nil || !strings.Contains(err.Error(), "rollback snapshot is missing") {
		t.Fatalf("waitForPaused() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("waitForPaused() took %s instead of returning the permanent failure", elapsed)
	}
}
