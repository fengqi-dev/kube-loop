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

func (r *TrafficBindingReconciler) setPending(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	return r.setLifecycleStatus(
		ctx,
		binding,
		trafficv1alpha1.TrafficBindingPhasePending,
		"FinalizerInstalled",
		"TrafficBinding is waiting for reconciliation",
		metav1.ConditionFalse,
	)
}

func (r *TrafficBindingReconciler) setReconciling(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	return r.setLifecycleStatus(
		ctx,
		binding,
		trafficv1alpha1.TrafficBindingPhaseReconciling,
		string(binding.Spec.Mode)+"Reconciling",
		"TrafficBinding is reconciling its desired state",
		metav1.ConditionTrue,
	)
}

func (r *TrafficBindingReconciler) persistSnapshot(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	snapshot *trafficv1alpha1.ServiceSnapshot,
) error {
	before := binding.DeepCopy()
	binding.Status.Snapshot = snapshot
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhaseReconciling
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.ServiceName = snapshot.ServiceName
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
		Reason: "SnapshotCaptured", Message: "Rollback snapshot was persisted before mutation",
		ObservedGeneration: binding.Generation,
	})
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionProgressing, Status: metav1.ConditionTrue,
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
	setAccepted(binding, metav1.ConditionTrue, "Accepted", "TrafficBinding specification is valid")
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionProgressing, Status: metav1.ConditionFalse,
		Reason: "Reconciled", Message: "TrafficBinding reached its desired state",
		ObservedGeneration: binding.Generation,
	})
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
	setCondition(
		binding,
		trafficv1alpha1.ConditionPaused,
		metav1.ConditionFalse,
		"Active",
		"TrafficBinding is active",
	)
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionRestored, Status: metav1.ConditionFalse,
		Reason: "Active", Message: "TrafficBinding is active and has not been restored",
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
	accepted := metav1.ConditionTrue
	acceptedReason := "Accepted"
	acceptedMessage := "TrafficBinding specification is valid"
	if reason == "InvalidSpec" {
		accepted = metav1.ConditionFalse
		acceptedReason = reason
		acceptedMessage = safeMessage(cause)
	}
	setAccepted(binding, accepted, acceptedReason, acceptedMessage)
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionProgressing, Status: metav1.ConditionFalse,
		Reason: reason, Message: safeMessage(cause), ObservedGeneration: binding.Generation,
	})
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionReady, Status: metav1.ConditionFalse,
		Reason: reason, Message: safeMessage(cause), ObservedGeneration: binding.Generation,
	})
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: trafficv1alpha1.ConditionDegraded, Status: metav1.ConditionTrue,
		Reason: reason, Message: safeMessage(cause), ObservedGeneration: binding.Generation,
	})
	setCondition(
		binding,
		trafficv1alpha1.ConditionPaused,
		metav1.ConditionFalse,
		reason,
		safeMessage(cause),
	)
	if equalityStatus(before.Status, binding.Status) {
		return nil
	}
	return r.Status().Patch(ctx, binding, client.MergeFrom(before))
}

func (r *TrafficBindingReconciler) setRestoring(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	return r.setLifecycleStatus(
		ctx,
		binding,
		trafficv1alpha1.TrafficBindingPhaseRestoring,
		"Restoring",
		"TrafficBinding resources are being restored",
		metav1.ConditionTrue,
	)
}

func (r *TrafficBindingReconciler) setPausing(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	return r.setLifecycleStatus(
		ctx,
		binding,
		trafficv1alpha1.TrafficBindingPhasePausing,
		"Pausing",
		"TrafficBinding resources are being restored before pausing",
		metav1.ConditionTrue,
	)
}

func (r *TrafficBindingReconciler) setPauseFailed(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	cause error,
) error {
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhasePausing
	message := safeMessage(cause)
	setAccepted(binding, metav1.ConditionTrue, "Accepted", "TrafficBinding specification is valid")
	setCondition(binding, trafficv1alpha1.ConditionProgressing, metav1.ConditionFalse, "PauseFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionReady, metav1.ConditionFalse, "PauseFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionDegraded, metav1.ConditionTrue, "PauseFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionPaused, metav1.ConditionFalse, "PauseFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionRestored, metav1.ConditionFalse, "PauseFailed", message)
	return r.patchStatus(ctx, binding, before)
}

func (r *TrafficBindingReconciler) setPaused(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhasePaused
	binding.Status.ServiceClusterIP = ""
	binding.Status.Snapshot = nil
	setAccepted(binding, metav1.ConditionTrue, "Accepted", "TrafficBinding specification is valid")
	setCondition(binding, trafficv1alpha1.ConditionProgressing, metav1.ConditionFalse, "Paused", "Pausing completed")
	setCondition(binding, trafficv1alpha1.ConditionReady, metav1.ConditionFalse, "Paused", "TrafficBinding is paused")
	setCondition(binding, trafficv1alpha1.ConditionDegraded, metav1.ConditionFalse, "Paused", "Pausing completed")
	setCondition(
		binding,
		trafficv1alpha1.ConditionPaused,
		metav1.ConditionTrue,
		"Paused",
		"TrafficBinding resources were restored",
	)
	setCondition(
		binding,
		trafficv1alpha1.ConditionRestored,
		metav1.ConditionFalse,
		"Paused",
		"TrafficBinding is retained",
	)
	return r.patchStatus(ctx, binding, before)
}

func (r *TrafficBindingReconciler) setRestoreFailed(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	cause error,
) error {
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhaseRestoring
	message := safeMessage(cause)
	setAccepted(binding, metav1.ConditionTrue, "Accepted", "TrafficBinding specification is valid")
	setCondition(
		binding,
		trafficv1alpha1.ConditionProgressing,
		metav1.ConditionFalse,
		"RestoreFailed",
		message,
	)
	setCondition(binding, trafficv1alpha1.ConditionReady, metav1.ConditionFalse, "RestoreFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionDegraded, metav1.ConditionTrue, "RestoreFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionPaused, metav1.ConditionFalse, "RestoreFailed", message)
	setCondition(binding, trafficv1alpha1.ConditionRestored, metav1.ConditionFalse, "RestoreFailed", message)
	return r.patchStatus(ctx, binding, before)
}

func (r *TrafficBindingReconciler) setRestored(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = trafficv1alpha1.TrafficBindingPhaseRestored
	setAccepted(binding, metav1.ConditionTrue, "Accepted", "TrafficBinding specification is valid")
	setCondition(
		binding,
		trafficv1alpha1.ConditionProgressing,
		metav1.ConditionFalse,
		"Restored",
		"Restoration completed",
	)
	setCondition(
		binding,
		trafficv1alpha1.ConditionReady,
		metav1.ConditionFalse,
		"Restored",
		"TrafficBinding is no longer active",
	)
	setCondition(
		binding,
		trafficv1alpha1.ConditionDegraded,
		metav1.ConditionFalse,
		"Restored",
		"Restoration completed",
	)
	setCondition(
		binding,
		trafficv1alpha1.ConditionRestored,
		metav1.ConditionTrue,
		"Restored",
		"TrafficBinding resources were restored",
	)
	setCondition(
		binding,
		trafficv1alpha1.ConditionPaused,
		metav1.ConditionFalse,
		"Restored",
		"TrafficBinding is being deleted",
	)
	return r.patchStatus(ctx, binding, before)
}

func (r *TrafficBindingReconciler) setLifecycleStatus(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	phase trafficv1alpha1.TrafficBindingPhase,
	reason, message string,
	progressing metav1.ConditionStatus,
) error {
	before := binding.DeepCopy()
	binding.Status.ObservedGeneration = binding.Generation
	binding.Status.Phase = phase
	setAccepted(binding, metav1.ConditionTrue, "Accepted", "TrafficBinding specification is valid")
	setCondition(binding, trafficv1alpha1.ConditionProgressing, progressing, reason, message)
	setCondition(binding, trafficv1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
	setCondition(binding, trafficv1alpha1.ConditionDegraded, metav1.ConditionFalse, reason, message)
	setCondition(binding, trafficv1alpha1.ConditionPaused, metav1.ConditionFalse, reason, message)
	setCondition(binding, trafficv1alpha1.ConditionRestored, metav1.ConditionFalse, reason, message)
	return r.patchStatus(ctx, binding, before)
}

func setAccepted(
	binding *trafficv1alpha1.TrafficBinding,
	status metav1.ConditionStatus,
	reason, message string,
) {
	setCondition(binding, trafficv1alpha1.ConditionAccepted, status, reason, message)
}

func setCondition(
	binding *trafficv1alpha1.TrafficBinding,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	apiMeta.SetStatusCondition(&binding.Status.Conditions, metav1.Condition{
		Type: conditionType, Status: status, Reason: reason, Message: message,
		ObservedGeneration: binding.Generation,
	})
}

func (r *TrafficBindingReconciler) patchStatus(
	ctx context.Context,
	binding, before *trafficv1alpha1.TrafficBinding,
) error {
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
