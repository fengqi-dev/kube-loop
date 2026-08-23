package controller

import (
	"context"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

func (r *TrafficBindingReconciler) persistSnapshot(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	snapshot *trafficv1alpha1.ServiceSnapshot,
) error {
	before := binding.DeepCopy()
	binding.Status.Snapshot = snapshot
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhasePending
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.ServiceName = snapshot.ServiceName
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
		Reason: "SnapshotCaptured", Message: "Rollback snapshot was persisted before mutation",
		ObservedGeneration: binding.Generation,
	})
	return r.Status().Patch(ctx, binding, client.MergeFrom(before))
}

func (r *TrafficBindingReconciler) setReady(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	before *trafficv1alpha1.TrafficBinding,
) error {
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhaseReady
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionReady, Status: metav1.ConditionTrue,
		Reason: string(binding.Spec.Mode) + "Ready", Message: "TrafficBinding reached its desired state",
		ObservedGeneration: binding.Generation,
	})
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionDegraded, Status: metav1.ConditionFalse,
		Reason: "Reconciled", Message: "No reconciliation error is active",
		ObservedGeneration: binding.Generation,
	})
	if equalityStatus(before.Status, binding.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, binding, client.MergeFrom(before)); err != nil {
		return err
	}
	r.event(binding, corev1.EventTypeNormal, "Ready", "TrafficBinding reached its desired state")
	return nil
}

func (r *TrafficBindingReconciler) setDegraded(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	reason string,
	cause error,
) error {
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhaseDegraded
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
		Reason: reason, Message: safeMessage(cause), ObservedGeneration: binding.Generation,
	})
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionDegraded, Status: metav1.ConditionTrue,
		Reason: reason, Message: safeMessage(cause), ObservedGeneration: binding.Generation,
	})
	if equalityStatus(before.Status, binding.Status) {
		return nil
	}
	return r.Status().Patch(ctx, binding, client.MergeFrom(before))
}

func equalityStatus(left, right trafficv1alpha1.TrafficBindingStatus) bool {
	return reflect.DeepEqual(left, right)
}

func reasonFor(err error) string {
	if isPermanent(err) {
		return "InvalidOrConflictingState"
	}
	if apierrors.IsNotFound(err) {
		return "TargetNotFound"
	}
	return "ReconcileFailed"
}

func safeMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func (r *TrafficBindingReconciler) event(
	binding *trafficv1alpha1.TrafficBinding,
	eventType, reason, message string,
) {
	if r.Recorder != nil {
		r.Recorder.Eventf(binding, nil, eventType, reason, reason, "%s", message)
	}
}
