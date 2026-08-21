package servicebinding

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCaptureServiceIntercept(t *testing.T) {
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
				Name: "api-xyz", Namespace: "default",
				Labels: map[string]string{interceptServiceNameLabel: "api"},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.244.0.5"}}},
			Ports: []discoveryv1.EndpointPort{{
				Name: new("http"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080)),
			}},
		},
		&corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Subsets: []corev1.EndpointSubset{{
				Addresses: []corev1.EndpointAddress{{IP: "10.244.0.5"}},
				Ports: []corev1.EndpointPort{{
					Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP,
				}},
			}},
		},
	)

	snapshot := &ServiceInterceptSnapshot{
		Namespace: "default", Service: "api", GatewayIP: "10.244.0.9",
		Ports: []InterceptPort{{
			Name: "http", Protocol: corev1.ProtocolTCP, ServicePort: 80, ListenPort: 20080,
		}},
	}
	if err := CaptureServiceIntercept(context.Background(), client, snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Selector["app"] != "api" {
		t.Fatalf("selector snapshot = %#v", snapshot.Selector)
	}
	if !snapshot.HasEndpointSlices || len(snapshot.EndpointSlices) != 1 ||
		snapshot.EndpointSlices[0].Endpoints[0].Addresses[0] != "10.244.0.5" {
		t.Fatalf("EndpointSlice snapshot = %#v", snapshot.EndpointSlices)
	}
	if !snapshot.HasEndpoints || len(snapshot.EndpointsSubsets) != 1 ||
		snapshot.EndpointsSubsets[0].Addresses[0].IP != "10.244.0.5" {
		t.Fatalf("Endpoints snapshot = %#v", snapshot.EndpointsSubsets)
	}
	if _, err := json.Marshal(snapshot); err != nil {
		t.Fatalf("snapshot is not persistable: %v", err)
	}

	service, err := client.CoreV1().
		Services("default").
		Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.Selector["app"] != "api" {
		t.Fatalf("capture mutated Service selector: %#v", service.Spec.Selector)
	}
	slices, err := client.DiscoveryV1().
		EndpointSlices("default").
		List(context.Background(), metav1.ListOptions{})
	if err != nil || len(slices.Items) != 1 || slices.Items[0].Name != "api-xyz" {
		t.Fatalf("capture mutated EndpointSlices: %#v, err=%v", slices.Items, err)
	}
}

func TestCaptureServiceInterceptSupportsLegacyEndpoints(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.10.30", Selector: map[string]string{"app": "legacy"},
				Ports: []corev1.ServicePort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}},
			},
		},
		&corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "default"},
			Subsets: []corev1.EndpointSubset{{
				Addresses: []corev1.EndpointAddress{{IP: "10.244.0.8"}},
			}},
		},
	)
	snapshot := &ServiceInterceptSnapshot{
		Namespace: "default", Service: "legacy", GatewayIP: "10.244.0.9",
		Ports: []InterceptPort{{Name: "http", ServicePort: 80, ListenPort: 20080}},
	}
	if err := CaptureServiceIntercept(context.Background(), client, snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.HasEndpointSlices || !snapshot.HasEndpoints ||
		len(snapshot.EndpointsSubsets) != 1 ||
		snapshot.EndpointsSubsets[0].Addresses[0].IP != "10.244.0.8" {
		t.Fatalf("legacy Endpoints snapshot = %#v", snapshot)
	}
}
