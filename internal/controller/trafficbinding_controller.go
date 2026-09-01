package controller

import (
	"context"
	"errors"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
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
	Recorder events.EventRecorder
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
		if binding.Status.Phase == trafficv1alpha1.TrafficBindingPhaseRestored {
			before := binding.DeepCopy()
			controllerutil.RemoveFinalizer(binding, bindingFinalizer)
			if err := r.Patch(ctx, binding, client.MergeFrom(before)); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			return ctrl.Result{}, nil
		}
		if binding.Status.Phase == trafficv1alpha1.TrafficBindingPhasePaused {
			if err := r.setRestored(ctx, binding); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Millisecond}, nil
		}
		if binding.Status.Phase != trafficv1alpha1.TrafficBindingPhaseRestoring {
			if err := r.setRestoring(ctx, binding); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Millisecond}, nil
		}
		if err := r.cleanupForDeletion(ctx, binding); err != nil {
			log.Error(err, "Could not restore TrafficBinding resources", "mode", binding.Spec.Mode)
			if statusErr := r.setRestoreFailed(ctx, binding, err); statusErr != nil {
				return ctrl.Result{}, errors.Join(err, statusErr)
			}
			r.event(binding, corev1.EventTypeWarning, "RestoreFailed", safeMessage(err))
			return ctrl.Result{}, err
		}
		if err := r.setRestored(ctx, binding); err != nil {
			return ctrl.Result{}, err
		}
		r.event(binding, corev1.EventTypeNormal, "Restored", "TrafficBinding resources were restored")
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
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
		if err := r.setPending(ctx, binding); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	if desiredState(binding) == trafficv1alpha1.TrafficBindingDesiredStatePaused {
		return r.reconcilePaused(ctx, binding)
	}
	if awaitingRelay(binding) {
		if binding.Status.Phase != trafficv1alpha1.TrafficBindingPhasePending ||
			binding.Status.ObservedGeneration != binding.Generation {
			if err := r.setPending(ctx, binding); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
	if binding.Status.Phase != trafficv1alpha1.TrafficBindingPhaseReady &&
		binding.Status.Phase != trafficv1alpha1.TrafficBindingPhaseReconciling {
		if err := r.setReconciling(ctx, binding); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}

	statusBase := binding.DeepCopy()
	result, complete, err := r.reconcileActive(ctx, binding)
	if err == nil {
		if !complete {
			return result, nil
		}
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

func awaitingRelay(binding *trafficv1alpha1.TrafficBinding) bool {
	return binding.Spec.Mode != trafficv1alpha1.TrafficBindingModePortForward &&
		binding.Spec.Relay == nil
}

func (r *TrafficBindingReconciler) reconcilePaused(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (ctrl.Result, error) {
	if binding.Status.Phase == trafficv1alpha1.TrafficBindingPhasePaused &&
		binding.Status.ObservedGeneration == binding.Generation {
		return ctrl.Result{}, nil
	}
	if binding.Status.Phase == trafficv1alpha1.TrafficBindingPhasePaused {
		// Session adoption updates transport metadata in spec and therefore the
		// Kubernetes generation. The workload is already restored in Paused, so
		// only acknowledge the new generation instead of attempting rollback a
		// second time after setPaused has discarded the completed snapshot.
		return ctrl.Result{}, r.setPaused(ctx, binding)
	}
	if binding.Status.Phase == trafficv1alpha1.TrafficBindingPhasePending ||
		binding.Status.Phase == "" {
		return ctrl.Result{}, r.setPaused(ctx, binding)
	}
	if binding.Status.Phase != trafficv1alpha1.TrafficBindingPhasePausing {
		if err := r.setPausing(ctx, binding); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	if err := r.cleanupForPause(ctx, binding); err != nil {
		if statusErr := r.setPauseFailed(ctx, binding, err); statusErr != nil {
			return ctrl.Result{}, errors.Join(err, statusErr)
		}
		r.event(binding, corev1.EventTypeWarning, "PauseFailed", safeMessage(err))
		return ctrl.Result{RequeueAfter: retryAfter}, err
	}
	if err := r.setPaused(ctx, binding); err != nil {
		return ctrl.Result{}, err
	}
	r.event(binding, corev1.EventTypeNormal, "Paused", "TrafficBinding resources were restored and paused")
	return ctrl.Result{}, nil
}

func (r *TrafficBindingReconciler) reconcileActive(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (ctrl.Result, bool, error) {
	switch binding.Spec.Mode {
	case trafficv1alpha1.TrafficBindingModePortForward:
		return ctrl.Result{RequeueAfter: targetRecheckAfter}, true, r.validateTarget(ctx, binding)
	case trafficv1alpha1.TrafficBindingModePreview:
		service, err := r.reconcilePreview(ctx, binding)
		if err == nil {
			binding.Status.ServiceName = service.Name
			binding.Status.ServiceClusterIP = service.Spec.ClusterIP
		}
		return ctrl.Result{}, true, err
	case trafficv1alpha1.TrafficBindingModeExchange, trafficv1alpha1.TrafficBindingModeMirror:
		if binding.Status.Snapshot == nil {
			snapshot, err := r.captureService(ctx, binding)
			if err != nil {
				return ctrl.Result{}, false, err
			}
			if err := r.persistSnapshot(ctx, binding, snapshot); err != nil {
				return ctrl.Result{}, false, err
			}
			return ctrl.Result{RequeueAfter: time.Millisecond}, false, nil
		}
		binding.Status.ServiceName = binding.Spec.Target.Name
		binding.Status.ServiceClusterIP = ""
		return ctrl.Result{}, true, r.reconcileIntercept(ctx, binding)
	default:
		return ctrl.Result{}, false, permanentf("spec.mode %q is unsupported", binding.Spec.Mode)
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
			return r.cleanupInterceptWithoutSnapshot(ctx, binding)
		}
		return r.restoreService(ctx, binding)
	default:
		return permanentf("spec.mode %q is unsupported", binding.Spec.Mode)
	}
}

func (r *TrafficBindingReconciler) cleanupForPause(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	return r.cleanup(ctx, binding)
}

func (r *TrafficBindingReconciler) cleanupForDeletion(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	if desiredState(binding) == trafficv1alpha1.TrafficBindingDesiredStatePaused {
		return r.cleanupForPause(ctx, binding)
	}
	return r.cleanup(ctx, binding)
}

func desiredState(
	binding *trafficv1alpha1.TrafficBinding,
) trafficv1alpha1.TrafficBindingDesiredState {
	if binding.Spec.DesiredState == "" {
		return trafficv1alpha1.TrafficBindingDesiredStateActive
	}
	return binding.Spec.DesiredState
}

func requestsForBinding(object client.Object) []reconcile.Request {
	name := object.GetAnnotations()[bindingNameAnnotation]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{
		Namespace: object.GetNamespace(), Name: name}}
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
