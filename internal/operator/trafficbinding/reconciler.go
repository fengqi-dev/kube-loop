package trafficbinding

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/internal/operator/api/v1alpha1"
)

const (
	bindingFinalizer      = "traffic.kubeloop.io/finalizer"
	bindingNameAnnotation = "traffic.kubeloop.io/binding-name"
	bindingUIDAnnotation  = "traffic.kubeloop.io/binding-uid"
	bindingModeAnnotation = "traffic.kubeloop.io/mode"
	bindingNameLabel      = "traffic.kubeloop.io/binding"
	managedByLabel        = "app.kubernetes.io/managed-by"
	managedByValue        = "kubeloop-operator"
	retryAfter            = 5 * time.Second
	targetRecheckAfter    = 30 * time.Second
)

// TrafficBindingReconciler reconciles a TrafficBinding object.
type TrafficBindingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=traffic.kubeloop.io,resources=trafficbindings,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=traffic.kubeloop.io,resources=trafficbindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=traffic.kubeloop.io,resources=trafficbindings/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services;endpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *TrafficBindingReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	binding := &trafficv1alpha1.TrafficBinding{}
	if err := r.Get(ctx, request.NamespacedName, binding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !binding.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(binding, bindingFinalizer) {
			return ctrl.Result{}, nil
		}
		if err := r.cleanup(ctx, binding); err != nil {
			log.Error(err, "Could not restore TrafficBinding resources", "mode", binding.Spec.Mode)
			r.event(binding, corev1.EventTypeWarning, "RestoreFailed", safeMessage(err))
			return ctrl.Result{}, err
		}
		before := binding.DeepCopy()
		controllerutil.RemoveFinalizer(binding, bindingFinalizer)
		if err := r.Patch(ctx, binding, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		r.event(binding, corev1.EventTypeNormal, "Restored", "TrafficBinding resources were restored")
		return ctrl.Result{}, nil
	}

	if err := validateBinding(binding); err != nil {
		_ = r.setDegraded(ctx, binding, "InvalidSpec", err)
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(binding, bindingFinalizer) {
		before := binding.DeepCopy()
		controllerutil.AddFinalizer(binding, bindingFinalizer)
		if err := r.Patch(ctx, binding, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	statusBase := binding.DeepCopy()
	result, err := r.reconcileActive(ctx, binding)
	if err == nil {
		if statusErr := r.setReady(ctx, binding, statusBase); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return result, nil
	}
	if statusErr := r.setDegraded(ctx, binding, reasonFor(err), err); statusErr != nil {
		return ctrl.Result{}, errors.Join(err, statusErr)
	}
	r.event(binding, corev1.EventTypeWarning, reasonFor(err), safeMessage(err))
	if isPermanent(err) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: retryAfter}, err
}

func (r *TrafficBindingReconciler) reconcileActive(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (ctrl.Result, error) {
	switch binding.Spec.Mode {
	case trafficv1alpha1.TrafficBindingModePortForward:
		return ctrl.Result{RequeueAfter: targetRecheckAfter}, r.validateTarget(ctx, binding)
	case trafficv1alpha1.TrafficBindingModePreview:
		service, err := r.reconcilePreview(ctx, binding)
		if err == nil {
			binding.Status.ServiceName = service.Name
			binding.Status.ServiceClusterIP = service.Spec.ClusterIP
		}
		return ctrl.Result{}, err
	case trafficv1alpha1.TrafficBindingModeExchange, trafficv1alpha1.TrafficBindingModeMirror:
		if binding.Status.Snapshot == nil {
			snapshot, err := r.captureService(ctx, binding)
			if err != nil {
				return ctrl.Result{}, err
			}
			if err := r.persistSnapshot(ctx, binding, snapshot); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		binding.Status.ServiceName = binding.Spec.Target.Name
		binding.Status.ServiceClusterIP = ""
		return ctrl.Result{}, r.reconcileIntercept(ctx, binding)
	default:
		return ctrl.Result{}, permanentf("spec.mode %q is unsupported", binding.Spec.Mode)
	}
}

func (r *TrafficBindingReconciler) cleanup(ctx context.Context, binding *trafficv1alpha1.TrafficBinding) error {
	switch binding.Spec.Mode {
	case trafficv1alpha1.TrafficBindingModePortForward:
		return nil
	case trafficv1alpha1.TrafficBindingModePreview:
		return r.deletePreview(ctx, binding)
	case trafficv1alpha1.TrafficBindingModeExchange, trafficv1alpha1.TrafficBindingModeMirror:
		if binding.Status.Snapshot == nil {
			return permanentf("rollback snapshot is missing")
		}
		return r.restoreService(ctx, binding)
	default:
		return permanentf("spec.mode %q is unsupported", binding.Spec.Mode)
	}
}

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
		r.Recorder.Event(binding, eventType, reason, message)
	}
}

func requestsForBinding(object client.Object) []reconcile.Request {
	name := object.GetAnnotations()[bindingNameAnnotation]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: object.GetNamespace(), Name: name,
	}}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *TrafficBindingReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).
		For(&trafficv1alpha1.TrafficBinding{}).
		Owns(&corev1.Service{}).
		Owns(&discoveryv1.EndpointSlice{}).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, object client.Object) []reconcile.Request {
				return requestsForBinding(object)
			},
		)).
		Watches(&corev1.Endpoints{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, object client.Object) []reconcile.Request {
				return requestsForBinding(object)
			},
		)).
		Named("trafficbinding").
		Complete(r)
}
