package servicebinding

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
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

	if err := deletePreviewService(context.Background(), client, snapshot); err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().Services("demo").Get(context.Background(), "local-api", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected deleted service, got %v", err)
	}
}
