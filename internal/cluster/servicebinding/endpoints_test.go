package servicebinding

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestApplyAndRestoreServiceIntercept(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.10.20",
				Selector:  map[string]string{"app": "api"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
		&discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-xyz",
				Namespace: "default",
				Labels:    map[string]string{interceptServiceNameLabel: "api"},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.5"}}},
			Ports: []discoveryv1.EndpointPort{{
				Name: new("http"), Protocol: endpointTestPtr(corev1.ProtocolTCP),
				Port: new(int32(8080)),
			}},
		},
	)

	snapshot := &ServiceInterceptSnapshot{
		Namespace: "default",
		Service:   "api",
		Selector:  map[string]string{"app": "api"},
		GatewayIP: "10.244.0.9",
		Ports: []InterceptPort{{
			Name: "http", Protocol: corev1.ProtocolTCP, ServicePort: 80, ListenPort: 20080,
		}},
	}
	if err := applyServiceIntercept(context.Background(), client, snapshot, "id-1"); err != nil {
		t.Fatal(err)
	}

	service, err := client.CoreV1().Services("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.Spec.Selector) != 0 {
		t.Fatalf("selector not cleared: %#v", service.Spec.Selector)
	}
	if service.Annotations[annotationInterceptID] != "id-1" {
		t.Fatalf("missing intercept annotation")
	}
	if !snapshot.HasEndpointSlices || len(snapshot.EndpointSlices) != 1 {
		t.Fatalf("endpoint slices not snapshotted: %#v", snapshot)
	}
	if snapshot.EndpointSlices[0].Endpoints[0].Addresses[0] != "10.244.0.5" {
		t.Fatalf("unexpected snapshotted address: %#v", snapshot.EndpointSlices)
	}

	slices, err := client.DiscoveryV1().EndpointSlices("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(slices.Items) != 1 {
		t.Fatalf("slice count=%d", len(slices.Items))
	}
	if slices.Items[0].Endpoints[0].Addresses[0] != "10.244.0.9" {
		t.Fatalf("gateway IP not applied")
	}
	if *slices.Items[0].Ports[0].Port != 20080 {
		t.Fatalf("listen port=%d", *slices.Items[0].Ports[0].Port)
	}

	if err := restoreServiceIntercept(context.Background(), client, *snapshot); err != nil {
		t.Fatal(err)
	}
	service, err = client.CoreV1().Services("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.Selector["app"] != "api" {
		t.Fatalf("selector not restored: %#v", service.Spec.Selector)
	}
	if service.Annotations[annotationInterceptID] != "" {
		t.Fatalf("intercept annotation still present")
	}

	restored, err := client.DiscoveryV1().EndpointSlices("default").Get(
		context.Background(), "api-xyz", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Endpoints) != 1 || restored.Endpoints[0].Addresses[0] != "10.244.0.5" {
		t.Fatalf("endpoint slice not restored: %#v", restored.Endpoints)
	}
	if len(restored.Ports) != 1 || *restored.Ports[0].Port != 8080 {
		t.Fatalf("endpoint slice ports not restored: %#v", restored.Ports)
	}

	_, err = client.DiscoveryV1().EndpointSlices("default").Get(
		context.Background(), managedEndpointSliceName("api"), metav1.GetOptions{},
	)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("managed slice should be deleted, got %v", err)
	}
}

//go:fix inline
func endpointTestPtr[T any](value T) *T {
	return new(value)
}

func TestApplyServiceInterceptRollsBackWhenManagedSliceCreationFails(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.10.20",
				Selector:  map[string]string{"app": "api"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
		&discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-original",
				Namespace: "default",
				Labels:    map[string]string{interceptServiceNameLabel: "api"},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints: []discoveryv1.Endpoint{{
				Addresses: []string{"10.244.0.5"},
			}},
		},
	)
	createErr := errors.New("managed slice create failed")
	client.PrependReactor(
		"create",
		"endpointslices",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			create := action.(k8stesting.CreateAction)
			slice := create.GetObject().(*discoveryv1.EndpointSlice)
			if slice.Name == managedEndpointSliceName("api") {
				return true, nil, createErr
			}
			return false, nil, nil
		},
	)

	snapshot := &ServiceInterceptSnapshot{
		Namespace: "default",
		Service:   "api",
		Selector:  map[string]string{"app": "api"},
		GatewayIP: "10.244.0.9",
		Ports: []InterceptPort{{
			Name: "http", Protocol: corev1.ProtocolTCP, ServicePort: 80, ListenPort: 20080,
		}},
	}
	if err := applyServiceIntercept(
		context.Background(), client, snapshot, "id-1",
	); !errors.Is(err, createErr) {
		t.Fatalf("apply error = %v, want %v", err, createErr)
	}

	service, err := client.CoreV1().Services("default").Get(
		context.Background(), "api", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.Selector["app"] != "api" {
		t.Fatalf("selector not rolled back: %#v", service.Spec.Selector)
	}
	if service.Annotations[annotationInterceptID] != "" {
		t.Fatalf("intercept annotation remains: %#v", service.Annotations)
	}
	restored, err := client.DiscoveryV1().EndpointSlices("default").Get(
		context.Background(), "api-original", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Endpoints[0].Addresses[0] != "10.244.0.5" {
		t.Fatalf("original endpoint slice not restored: %#v", restored.Endpoints)
	}
}

func TestBuildInterceptPorts(t *testing.T) {
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
				{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP},
			},
		},
	}
	next := int32(20000)
	ports, err := BuildInterceptPorts(service, func(corev1.Protocol) (int32, error) {
		next++
		return next, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0].ListenPort != 20001 || ports[1].ListenPort != 20002 {
		t.Fatalf("unexpected ports %#v", ports)
	}
}
