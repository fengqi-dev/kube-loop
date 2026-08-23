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
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
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
			return ctrl.Result{RequeueAfter: time.Millisecond}, nil
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
