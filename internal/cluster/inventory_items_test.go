package cluster

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServiceInfoFromCoreIncludesType(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.96.0.8",
			Ports:     []corev1.ServicePort{{Port: 8080}},
		},
	}

	info, ok := serviceInfoFromCore(service)
	if !ok {
		t.Fatal("expected service to be included")
	}
	if info.Type != string(corev1.ServiceTypeNodePort) {
		t.Fatalf("type = %q, want %q", info.Type, corev1.ServiceTypeNodePort)
	}
}

func TestServiceInfoFromCoreDefaultsType(t *testing.T) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.8",
			Ports:     []corev1.ServicePort{{Port: 8080}},
		},
	}

	info, ok := serviceInfoFromCore(service)
	if !ok {
		t.Fatal("expected service to be included")
	}
	if info.Type != string(corev1.ServiceTypeClusterIP) {
		t.Fatalf("type = %q, want %q", info.Type, corev1.ServiceTypeClusterIP)
	}
}
