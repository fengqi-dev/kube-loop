package servicebinding

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCreateAndDeletePreviewService(t *testing.T) {
	client := fake.NewSimpleClientset()
	snapshot := PreviewServiceSnapshot{
		Namespace: "demo",
		Service:   "local-api",
		GatewayIP: "10.244.0.9",
		Ports: []InterceptPort{{
			Name: "tcp-8080", Protocol: corev1.ProtocolTCP, ServicePort: 8080, ListenPort: 20100,
		}},
	}
	service, err := createPreviewService(context.Background(), client, snapshot, "demo/local-api")
	if err != nil {
		t.Fatal(err)
	}
	if service.Spec.ClusterIP == "" && service.Name != "local-api" {
		t.Fatalf("unexpected service: %#v", service)
	}
	if len(service.Spec.Selector) != 0 {
		t.Fatalf("preview service must have empty selector")
	}
	if service.Annotations[annotationPreviewID] != "demo/local-api" {
		t.Fatalf("missing preview annotation")
	}

	slices, err := client.DiscoveryV1().EndpointSlices("demo").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(slices.Items) != 1 {
		t.Fatalf("slice count=%d", len(slices.Items))
	}
	if slices.Items[0].Endpoints[0].Addresses[0] != "10.244.0.9" {
		t.Fatalf("gateway IP not applied")
	}

	if service.Labels[previewOwnerLabel] != previewOwnerLabelValue("demo/local-api") {
		t.Fatalf("missing hashed preview owner label: %#v", service.Labels)
	}
	if slices.Items[0].Annotations[annotationPreviewID] != "demo/local-api" ||
		slices.Items[0].Labels[previewOwnerLabel] != previewOwnerLabelValue("demo/local-api") {
		t.Fatalf("EndpointSlice owner metadata=%#v %#v", slices.Items[0].Labels, slices.Items[0].Annotations)
	}

	if err := deletePreviewService(context.Background(), client, snapshot, "demo/local-api"); err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().Services("demo").Get(context.Background(), "local-api", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted service, got %v", err)
	}
}

func TestCreatePreviewNeverOverwritesExistingService(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "local-api", Namespace: "demo", Labels: map[string]string{"user": "owned"}},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}}},
	})
	snapshot := previewTestSnapshot()
	if _, err := createPreviewService(context.Background(), client, snapshot, "task-one"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create over existing Service err=%v", err)
	}
	service, err := client.CoreV1().Services("demo").Get(context.Background(), "local-api", metav1.GetOptions{})
	if err != nil || service.Labels["user"] != "owned" || service.Annotations[annotationPreviewID] != "" {
		t.Fatalf("existing Service was changed: %#v err=%v", service, err)
	}
}

func TestCreatePreviewNeverOverwritesExistingEndpointSlice(t *testing.T) {
	foreign := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: managedEndpointSliceName("local-api"), Namespace: "demo",
			Labels: map[string]string{ServiceNameLabel: "local-api", "user": "owned"},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	client := fake.NewSimpleClientset(foreign)
	snapshot := previewTestSnapshot()
	if _, err := createPreviewService(context.Background(), client, snapshot, "task-one"); err == nil || !strings.Contains(err.Error(), "endpoint slice") {
		t.Fatalf("create over existing EndpointSlice err=%v", err)
	}
	if _, err := client.CoreV1().Services("demo").Get(context.Background(), "local-api", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("partially created Service was not compensated: %v", err)
	}
	slice, err := client.DiscoveryV1().EndpointSlices("demo").Get(
		context.Background(), foreign.Name, metav1.GetOptions{},
	)
	if err != nil || slice.Labels["user"] != "owned" || slice.Annotations[annotationPreviewID] != "" {
		t.Fatalf("existing EndpointSlice was changed: %#v err=%v", slice, err)
	}
}

func TestDeletePreviewOnlyRemovesExactOwner(t *testing.T) {
	client := fake.NewSimpleClientset()
	snapshot := previewTestSnapshot()
	if _, err := createPreviewService(context.Background(), client, snapshot, "task-one"); err != nil {
		t.Fatal(err)
	}
	if err := deletePreviewService(context.Background(), client, snapshot, "task-two"); err != nil {
		t.Fatalf("delete with wrong owner must be an idempotent no-op: %v", err)
	}
	if _, err := client.CoreV1().Services("demo").Get(context.Background(), "local-api", metav1.GetOptions{}); err != nil {
		t.Fatalf("foreign Service was deleted: %v", err)
	}
	if _, err := client.DiscoveryV1().EndpointSlices("demo").Get(
		context.Background(), managedEndpointSliceName("local-api"), metav1.GetOptions{},
	); err != nil {
		t.Fatalf("foreign EndpointSlice was deleted: %v", err)
	}
}

func previewTestSnapshot() PreviewServiceSnapshot {
	return PreviewServiceSnapshot{
		Namespace: "demo", Service: "local-api", GatewayIP: "10.244.0.9",
		Ports: []InterceptPort{{
			Name: "http", Protocol: corev1.ProtocolTCP, ServicePort: 80, ListenPort: 20100,
		}},
	}
}
