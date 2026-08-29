package controller

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // Ginkgo's test DSL intentionally uses dot imports.
	. "github.com/onsi/gomega"    //nolint:revive // Gomega matchers are the companion Ginkgo DSL.
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
)

const testNamespace = "default"

var _ = Describe("TrafficBinding Controller", func() {
	var reconciler *TrafficBindingReconciler

	BeforeEach(func() {
		reconciler = &TrafficBindingReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: events.NewFakeRecorder(64),
		}
	})

	It("creates and removes Preview resources with exact ownership", func(ctx SpecContext) {
		binding := previewBinding("preview-binding", "preview-service")
		Expect(k8sClient.Create(ctx, binding)).To(Succeed())
		DeferCleanup(deleteBinding, binding.Name)

		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
		pending := getBinding(ctx, binding.Name)
		Expect(pending.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhasePending))
		Expect(apiMeta.IsStatusConditionTrue(
			pending.Status.Conditions,
			trafficv1alpha1.ConditionAccepted,
		)).To(BeTrue())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
		reconciling := getBinding(ctx, binding.Name)
		Expect(reconciling.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseReconciling))
		Expect(apiMeta.IsStatusConditionTrue(
			reconciling.Status.Conditions,
			trafficv1alpha1.ConditionProgressing,
		)).To(BeTrue())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
		service := &corev1.Service{}
		Expect(k8sClient.Get(ctx, objectKey(binding.Spec.Preview.ServiceName), service)).To(Succeed())
		Expect(service.Spec.Selector).To(BeEmpty())
		Expect(service.Spec.Ports).To(HaveLen(2))
		Expect(service.Spec.Ports[0].Port).To(Equal(int32(8080)))
		Expect(service.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt32(32001)))
		Expect(service.Annotations[bindingNameAnnotation]).To(Equal(binding.Name))

		slices := &discoveryv1.EndpointSliceList{}
		Expect(k8sClient.List(ctx, slices, client.InNamespace(testNamespace), client.MatchingLabels{
			serviceNameLabel: binding.Spec.Preview.ServiceName,
		})).To(Succeed())
		Expect(slices.Items).To(HaveLen(1))
		Expect(slices.Items[0].Endpoints[0].Addresses).To(Equal([]string{"10.0.0.8"}))
		Expect(*slices.Items[0].Ports[0].Port).To(Equal(int32(32001)))
		Expect(*slices.Items[0].Ports[1].Port).To(Equal(int32(32002)))

		current := getBinding(ctx, binding.Name)
		Expect(current.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseReady))
		Expect(current.Status.ServiceName).To(Equal("preview-service"))
		Expect(apiMeta.IsStatusConditionTrue(current.Status.Conditions, trafficv1alpha1.ConditionReady)).To(BeTrue())
		Expect(apiMeta.IsStatusConditionFalse(
			current.Status.Conditions,
			trafficv1alpha1.ConditionProgressing,
		)).To(BeTrue())

		service.Spec.Selector = map[string]string{"drifted": "true"}
		service.Spec.Ports[0].TargetPort = intstr.FromInt32(39999)
		delete(service.Annotations, bindingModeAnnotation)
		Expect(k8sClient.Update(ctx, service)).To(Succeed())
		driftedSlice := &slices.Items[0]
		driftedSlice.Labels[serviceNameLabel] = "wrong-service"
		driftedSlice.Endpoints[0].Addresses = []string{"10.0.0.99"}
		*driftedSlice.Ports[0].Port = 39999
		Expect(k8sClient.Update(ctx, driftedSlice)).To(Succeed())
		_, err := reconciler.reconcilePreview(ctx, current)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, objectKey(service.Name), service)).To(Succeed())
		Expect(service.Spec.Selector).To(BeEmpty())
		Expect(service.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt32(32001)))
		Expect(service.Annotations[bindingModeAnnotation]).To(Equal(string(binding.Spec.Mode)))
		Expect(k8sClient.Get(ctx, objectKey(driftedSlice.Name), driftedSlice)).To(Succeed())
		Expect(driftedSlice.Labels[serviceNameLabel]).To(Equal(service.Name))
		Expect(driftedSlice.Endpoints[0].Addresses).To(Equal([]string{"10.0.0.8"}))
		Expect(*driftedSlice.Ports[0].Port).To(Equal(int32(32001)))

		Expect(k8sClient.Delete(ctx, current)).To(Succeed())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
		restoring := getBinding(ctx, binding.Name)
		Expect(restoring.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseRestoring))
		Expect(apiMeta.IsStatusConditionTrue(
			restoring.Status.Conditions,
			trafficv1alpha1.ConditionProgressing,
		)).To(BeTrue())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
		restored := getBinding(ctx, binding.Name)
		Expect(restored.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseRestored))
		Expect(apiMeta.IsStatusConditionTrue(
			restored.Status.Conditions,
			trafficv1alpha1.ConditionRestored,
		)).To(BeTrue())
		Expect(k8sClient.Get(ctx, objectKey("preview-service"), &corev1.Service{})).To(Satisfy(apierrors.IsNotFound))
		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
	})

	It("refuses to adopt a foreign Preview Service", func(ctx SpecContext) {
		service := &corev1.Service{
			Name: "occupied-preview", Namespace: testNamespace,
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "foreign"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		Expect(k8sClient.Create(ctx, service)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), service) })
		binding := previewBinding("foreign-preview-binding", service.Name)
		Expect(k8sClient.Create(ctx, binding)).To(Succeed())
		DeferCleanup(deleteBinding, binding.Name)
		binding = getBinding(ctx, binding.Name)
		_, err := reconciler.reconcilePreview(ctx, binding)
		Expect(err).To(MatchError(ContainSubstring("is not owned by this TrafficBinding")))
		Expect(k8sClient.Get(ctx, objectKey(service.Name), service)).To(Succeed())
		Expect(service.Spec.Selector).To(Equal(map[string]string{"app": "foreign"}))
	})

	DescribeTable("captures, applies and restores an intercepted Service",
		func(ctx SpecContext, mode trafficv1alpha1.TrafficBindingMode) {
			suffix := strings.ToLower(string(mode))
			serviceName := "backend-" + suffix
			bindingName := "binding-" + suffix
			service := &corev1.Service{
				Name: serviceName, Namespace: testNamespace,
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app": suffix},
					Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}},
				},
			}
			Expect(k8sClient.Create(ctx, service)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), service) })
			originalSlice := &discoveryv1.EndpointSlice{
				Name: "original-" + suffix, Namespace: testNamespace,
				Labels:      map[string]string{serviceNameLabel: serviceName},
				AddressType: discoveryv1.AddressTypeIPv4,
				Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.9"}}},
				Ports: []discoveryv1.EndpointPort{
					{Name: new("http"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080))},
				},
			}
			Expect(k8sClient.Create(ctx, originalSlice)).To(Succeed())
			legacy := &corev1.Endpoints{
				Name: serviceName, Namespace: testNamespace,
				Subsets: []corev1.EndpointSubset{{
					Addresses: []corev1.EndpointAddress{{IP: "10.244.0.9"}},
					Ports:     []corev1.EndpointPort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}},
				}},
			}
			Expect(k8sClient.Create(ctx, legacy)).To(Succeed())

			binding := interceptBinding(bindingName, serviceName, mode)
			Expect(k8sClient.Create(ctx, binding)).To(Succeed())
			DeferCleanup(deleteBinding, binding.Name)
			reconcileSuccessfully(ctx, reconciler, binding.Name, 4)

			currentService := &corev1.Service{}
			Expect(k8sClient.Get(ctx, objectKey(serviceName), currentService)).To(Succeed())
			Expect(currentService.Spec.Selector).To(BeEmpty())
			Expect(currentService.Annotations[bindingNameAnnotation]).To(Equal(bindingName))
			Expect(k8sClient.Get(ctx, objectKey(legacy.Name), &corev1.Endpoints{})).To(Satisfy(apierrors.IsNotFound))
			slices := &discoveryv1.EndpointSliceList{}
			Expect(
				k8sClient.List(
					ctx,
					slices,
					client.InNamespace(testNamespace),
					client.MatchingLabels{serviceNameLabel: serviceName},
				),
			).To(Succeed())
			Expect(slices.Items).To(HaveLen(1))
			Expect(slices.Items[0].Name).NotTo(Equal(originalSlice.Name))
			Expect(slices.Items[0].Endpoints[0].Addresses).To(Equal([]string{"10.0.0.8"}))
			Expect(*slices.Items[0].Ports[0].Name).To(Equal("http"))

			currentBinding := getBinding(ctx, bindingName)
			Expect(currentBinding.Status.Snapshot).NotTo(BeNil())
			Expect(currentBinding.Status.Snapshot.ServiceUID).To(Equal(currentService.UID))
			Expect(currentBinding.Status.Snapshot.HadEndpointSlices).To(BeTrue())
			Expect(currentBinding.Status.Snapshot.HadEndpoints).To(BeTrue())
			staleSlice := &discoveryv1.EndpointSlice{
				Name: originalSlice.Name, Namespace: testNamespace,
				Labels:      map[string]string{serviceNameLabel: serviceName},
				AddressType: discoveryv1.AddressTypeIPv4,
				Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.99"}}},
				Ports: []discoveryv1.EndpointPort{
					{Name: new("wrong"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(9999))},
				},
			}
			Expect(k8sClient.Create(ctx, staleSlice)).To(Succeed())
			Expect(k8sClient.Delete(ctx, currentBinding)).To(Succeed())
			reconcileSuccessfully(ctx, reconciler, bindingName, 1)
			Expect(getBinding(ctx, bindingName).Status.Phase).To(Equal(
				trafficv1alpha1.TrafficBindingPhaseRestoring,
			))
			reconcileSuccessfully(ctx, reconciler, bindingName, 1)
			restoredBinding := getBinding(ctx, bindingName)
			Expect(restoredBinding.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseRestored))
			Expect(apiMeta.IsStatusConditionTrue(
				restoredBinding.Status.Conditions,
				trafficv1alpha1.ConditionRestored,
			)).To(BeTrue())

			restored := &corev1.Service{}
			Expect(k8sClient.Get(ctx, objectKey(serviceName), restored)).To(Succeed())
			Expect(restored.Spec.Selector).To(Equal(map[string]string{"app": suffix}))
			Expect(restored.Annotations).NotTo(HaveKey(bindingUIDAnnotation))
			restoredSlice := &discoveryv1.EndpointSlice{}
			Expect(k8sClient.Get(ctx, objectKey(originalSlice.Name), restoredSlice)).To(Succeed())
			Expect(restoredSlice.Endpoints[0].Addresses).To(Equal([]string{"10.244.0.9"}))
			Expect(*restoredSlice.Ports[0].Name).To(Equal("http"))
			Expect(*restoredSlice.Ports[0].Port).To(Equal(int32(8080)))
			restoredEndpoints := &corev1.Endpoints{}
			Expect(k8sClient.Get(ctx, objectKey(serviceName), restoredEndpoints)).To(Succeed())
			Expect(restoredEndpoints.Subsets).To(HaveLen(1))
			reconcileSuccessfully(ctx, reconciler, bindingName, 1)
		},
		Entry("Exchange", trafficv1alpha1.TrafficBindingModeExchange),
		Entry("Mirror", trafficv1alpha1.TrafficBindingModeMirror),
	)

	It("validates a PortForward target without creating Kubernetes resources", func(ctx SpecContext) {
		pod := &corev1.Pod{
			Name: "forward-target", Namespace: testNamespace,
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "example.invalid/app:test"}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), pod) })
		binding := portForwardBinding("port-forward-binding", pod.Name)
		Expect(k8sClient.Create(ctx, binding)).To(Succeed())
		DeferCleanup(deleteBinding, binding.Name)
		reconcileSuccessfully(ctx, reconciler, binding.Name, 3)
		Expect(getBinding(ctx, binding.Name).Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseReady))

		Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, requestFor(binding.Name))
		Expect(err).To(HaveOccurred())
		Expect(getBinding(ctx, binding.Name).Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseDegraded))
	})

	It("deletes an Exchange that failed before mutating its Service", func(ctx SpecContext) {
		binding := interceptBinding(
			"unmutated-exchange",
			"missing-service",
			trafficv1alpha1.TrafficBindingModeExchange,
		)
		Expect(k8sClient.Create(ctx, binding)).To(Succeed())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 2)
		_, err := reconciler.Reconcile(ctx, requestFor(binding.Name))
		Expect(err).To(HaveOccurred())
		degraded := getBinding(ctx, binding.Name)
		Expect(degraded.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseDegraded))
		Expect(degraded.Status.Snapshot).To(BeNil())
		Expect(degraded.Status.ServiceName).To(BeEmpty())

		Expect(k8sClient.Delete(ctx, degraded)).To(Succeed())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 3)
		Expect(k8sClient.Get(
			ctx,
			objectKey(binding.Name),
			&trafficv1alpha1.TrafficBinding{},
		)).To(Satisfy(apierrors.IsNotFound))
	})

	It("pauses, survives an Operator restart and activates again", func(ctx SpecContext) {
		binding := previewBinding("restart-preview-binding", "restart-preview-service")
		Expect(k8sClient.Create(ctx, binding)).To(Succeed())
		DeferCleanup(deleteBinding, binding.Name)
		reconcileSuccessfully(ctx, reconciler, binding.Name, 3)
		Expect(getBinding(ctx, binding.Name).Status.Phase).To(Equal(
			trafficv1alpha1.TrafficBindingPhaseReady,
		))

		stopping := getBinding(ctx, binding.Name)
		stopping.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
		Expect(k8sClient.Update(ctx, stopping)).To(Succeed())
		reconcileSuccessfully(ctx, reconciler, binding.Name, 1)
		Expect(getBinding(ctx, binding.Name).Status.Phase).To(Equal(
			trafficv1alpha1.TrafficBindingPhasePausing,
		))

		restarted := &TrafficBindingReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: events.NewFakeRecorder(64),
		}
		reconcileSuccessfully(ctx, restarted, binding.Name, 1)
		stopped := getBinding(ctx, binding.Name)
		Expect(stopped.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhasePaused))
		Expect(stopped.Status.ObservedGeneration).To(Equal(stopped.Generation))
		Expect(apiMeta.IsStatusConditionTrue(
			stopped.Status.Conditions,
			trafficv1alpha1.ConditionPaused,
		)).To(BeTrue())
		Expect(k8sClient.Get(
			ctx,
			objectKey(binding.Spec.Preview.ServiceName),
			&corev1.Service{},
		)).To(Satisfy(apierrors.IsNotFound))

		stopped.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStateActive
		Expect(k8sClient.Update(ctx, stopped)).To(Succeed())
		reconcileSuccessfully(ctx, restarted, binding.Name, 2)
		active := getBinding(ctx, binding.Name)
		Expect(active.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseReady))
		Expect(apiMeta.IsStatusConditionFalse(
			active.Status.Conditions,
			trafficv1alpha1.ConditionPaused,
		)).To(BeTrue())
		Expect(k8sClient.Get(
			ctx,
			objectKey(binding.Spec.Preview.ServiceName),
			&corev1.Service{},
		)).To(Succeed())

		active.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
		Expect(k8sClient.Update(ctx, active)).To(Succeed())
		reconcileSuccessfully(ctx, restarted, binding.Name, 2)
		stopped = getBinding(ctx, binding.Name)
		Expect(stopped.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhasePaused))
		Expect(k8sClient.Delete(ctx, stopped)).To(Succeed())
		reconcileSuccessfully(ctx, restarted, binding.Name, 2)
		Expect(k8sClient.Get(
			ctx,
			objectKey(binding.Name),
			&trafficv1alpha1.TrafficBinding{},
		)).To(Satisfy(apierrors.IsNotFound))
	})

	It("completes the Controller TrafficBinding activation and deletion contract", func(ctx SpecContext) {
		pod := &corev1.Pod{
			Name: "controller-contract-target", Namespace: testNamespace,
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app", Image: "example.invalid/app:test"}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), pod) })

		manager, err := trafficbindingclient.New(
			k8sClient,
			trafficbindingclient.Config{PollInterval: 10 * time.Millisecond},
		)
		Expect(err).NotTo(HaveOccurred())
		desired := portForwardBinding("", pod.Name)
		desired.Spec.SessionID = "77777777-7777-4777-8777-777777777777"
		desired.Spec.TaskID = "88888888-8888-4888-8888-888888888888"
		bindingName, err := trafficbindingclient.NameForTask(desired.Spec.TaskID)
		Expect(err).NotTo(HaveOccurred())

		type activationResult struct {
			binding *trafficv1alpha1.TrafficBinding
			managed bool
			err     error
		}
		activated := make(chan activationResult, 1)
		go func() {
			binding, managed, activateErr := manager.Activate(ctx, desired)
			activated <- activationResult{binding: binding, managed: managed, err: activateErr}
		}()
		Eventually(func() error {
			return k8sClient.Get(ctx, objectKey(bindingName), &trafficv1alpha1.TrafficBinding{})
		}).Should(Succeed())
		reconcileSuccessfully(ctx, reconciler, bindingName, 3)
		var result activationResult
		Eventually(activated).Should(Receive(&result))
		Expect(result.err).NotTo(HaveOccurred())
		Expect(result.managed).To(BeTrue())
		Expect(result.binding.Status.Phase).To(Equal(trafficv1alpha1.TrafficBindingPhaseReady))

		deleted := make(chan error, 1)
		go func() { deleted <- manager.Delete(ctx, testNamespace, desired.Spec.TaskID) }()
		Eventually(func() bool {
			binding := &trafficv1alpha1.TrafficBinding{}
			return k8sClient.Get(ctx, objectKey(bindingName), binding) == nil && !binding.DeletionTimestamp.IsZero()
		}).Should(BeTrue())
		reconcileSuccessfully(ctx, reconciler, bindingName, 3)
		Eventually(deleted).Should(Receive(Succeed()))
		Expect(
			k8sClient.Get(ctx, objectKey(bindingName), &trafficv1alpha1.TrafficBinding{}),
		).To(Satisfy(apierrors.IsNotFound))
	})

	It("enforces mode shape and immutable spec in the CRD", func(ctx SpecContext) {
		invalid := portForwardBinding("invalid-shape", "pod")
		invalid.Spec.Relay = &trafficv1alpha1.RelayEndpoint{Address: "10.0.0.8"}
		Expect(k8sClient.Create(ctx, invalid)).To(Satisfy(apierrors.IsInvalid))

		valid := portForwardBinding("immutable-spec", "pod")
		Expect(k8sClient.Create(ctx, valid)).To(Succeed())
		DeferCleanup(deleteBinding, valid.Name)
		stored := getBinding(ctx, valid.Name)
		Expect(stored.Spec.Ports[0].Protocol).To(Equal(trafficv1alpha1.TransportProtocolTCP))
		stored.Spec.Ports[0].TargetPort = 9090
		Expect(k8sClient.Update(ctx, stored)).To(Satisfy(apierrors.IsInvalid))
		stored = getBinding(ctx, valid.Name)
		stored.Spec.DesiredState = trafficv1alpha1.TrafficBindingDesiredStatePaused
		Expect(k8sClient.Update(ctx, stored)).To(Succeed())
	})
})

func previewBinding(name, service string) *trafficv1alpha1.TrafficBinding {
	return &trafficv1alpha1.TrafficBinding{
		Name: name, Namespace: testNamespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         trafficv1alpha1.TrafficBindingModePreview,
			SessionID:    "11111111-1111-4111-8111-111111111111",
			TaskID:       "22222222-2222-4222-8222-222222222222", SessionGeneration: 1,
			Relay:   &trafficv1alpha1.RelayEndpoint{Address: "10.0.0.8"},
			Preview: &trafficv1alpha1.PreviewExposure{ServiceName: service},
			Ports: []trafficv1alpha1.TrafficPort{
				{
					Name:       "http",
					TargetPort: 8080,
					RelayPort:  new(int32(32001)),
					Protocol:   trafficv1alpha1.TransportProtocolTCP,
				},
				{
					Name:       "dns",
					TargetPort: 5353,
					RelayPort:  new(int32(32002)),
					Protocol:   trafficv1alpha1.TransportProtocolUDP,
				},
			},
		},
	}
}

func interceptBinding(name, service string, mode trafficv1alpha1.TrafficBindingMode) *trafficv1alpha1.TrafficBinding {
	return &trafficv1alpha1.TrafficBinding{
		Name: name, Namespace: testNamespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         mode,
			SessionID:    "33333333-3333-4333-8333-333333333333",
			TaskID:       "44444444-4444-4444-8444-444444444444", SessionGeneration: 1,
			Target: &trafficv1alpha1.TrafficTarget{Kind: trafficv1alpha1.TargetKindService, Name: service},
			Relay:  &trafficv1alpha1.RelayEndpoint{Address: "10.0.0.8"},
			Ports: []trafficv1alpha1.TrafficPort{
				{
					Name:       "http",
					TargetPort: 8080,
					RelayPort:  new(int32(32002)),
					Protocol:   trafficv1alpha1.TransportProtocolTCP,
				},
			},
		},
	}
}

func portForwardBinding(name, pod string) *trafficv1alpha1.TrafficBinding {
	return &trafficv1alpha1.TrafficBinding{
		Name: name, Namespace: testNamespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         trafficv1alpha1.TrafficBindingModePortForward,
			SessionID:    "55555555-5555-4555-8555-555555555555",
			TaskID:       "66666666-6666-4666-8666-666666666666", SessionGeneration: 1,
			Target: &trafficv1alpha1.TrafficTarget{Kind: trafficv1alpha1.TargetKindPod, Name: pod},
			Ports:  []trafficv1alpha1.TrafficPort{{TargetPort: 8080, Protocol: trafficv1alpha1.TransportProtocolTCP}},
		},
	}
}

func reconcileSuccessfully(ctx context.Context, reconciler *TrafficBindingReconciler, name string, count int) {
	for range count {
		_, err := reconciler.Reconcile(ctx, requestFor(name))
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}
}

func requestFor(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: objectKey(name)}
}

func objectKey(name string) types.NamespacedName {
	return types.NamespacedName{Namespace: testNamespace, Name: name}
}

func getBinding(ctx context.Context, name string) *trafficv1alpha1.TrafficBinding {
	binding := &trafficv1alpha1.TrafficBinding{}
	ExpectWithOffset(1, k8sClient.Get(ctx, objectKey(name), binding)).To(Succeed())
	return binding
}

func deleteBinding(name string) {
	ctx := context.Background()
	binding := &trafficv1alpha1.TrafficBinding{}
	if err := k8sClient.Get(ctx, objectKey(name), binding); err != nil {
		return
	}
	if binding.DeletionTimestamp.IsZero() {
		_ = k8sClient.Delete(ctx, binding)
	}
	reconciler := &TrafficBindingReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	for range 3 {
		if _, err := reconciler.Reconcile(ctx, requestFor(name)); err != nil {
			return
		}
	}
}
