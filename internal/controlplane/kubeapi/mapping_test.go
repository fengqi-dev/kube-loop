package kubeapi

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func TestPodFromKubernetesMapsRuntimeState(t *testing.T) {
	created := metav1.NewTime(time.Now().Add(-time.Minute))
	pod := &corev1.Pod{
		Name: "api-0", Namespace: "default", CreationTimestamp: created,
		Spec: corev1.PodSpec{
			NodeName: "worker-a",
			Containers: []corev1.Container{
				{
					Name: "api",
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
					},
				},
				{Name: "sidecar"},
			},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      "10.0.0.12",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "api", Ready: true, RestartCount: 2},
				{Name: "sidecar", RestartCount: 1},
			},
		},
	}
	document := podFromKubernetes(pod)
	if document.Name != "api-0" || document.Namespace != "default" || document.Phase != "Running" ||
		document.PodIP != "10.0.0.12" || document.NodeName != "worker-a" || !document.Ready ||
		document.ReadyContainers != 1 || document.TotalContainers != 2 || document.Restarts != 3 ||
		document.AgeSeconds < 0 || !slices.Equal(document.Containers, []string{"api", "sidecar"}) {
		t.Fatalf("Pod document = %#v", document)
	}
	if len(document.Ports) != 1 || document.Ports[0].Name != "http" || document.Ports[0].Port != 8080 ||
		document.Ports[0].Protocol != "TCP" {
		t.Fatalf("Pod ports = %#v", document.Ports)
	}
}

func TestServiceFromKubernetesCombinesExternalAddresses(t *testing.T) {
	service := &corev1.Service{
		Name: "api", Namespace: "default", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute)),
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.96.0.1",
			ExternalName: "api.example.test", ExternalIPs: []string{"192.0.2.10"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8080),
			}},
		},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{
			{IP: "198.51.100.10"}, {Hostname: "lb.example.test"}, {},
		}}},
	}
	document := serviceFromKubernetes(service)
	expectedAddresses := []string{"192.0.2.10", "198.51.100.10", "lb.example.test", "api.example.test"}
	if document.Name != "api" || document.Namespace != "default" || document.Type != "LoadBalancer" ||
		document.ClusterIP != "10.96.0.1" || document.ExternalName != "api.example.test" ||
		!slices.Equal(document.ExternalIPs, expectedAddresses) || document.AgeSeconds < 0 {
		t.Fatalf("Service document = %#v", document)
	}
	if len(document.Ports) != 1 || document.Ports[0].TargetPort != "8080" {
		t.Fatalf("Service ports = %#v", document.Ports)
	}
}

func TestResourceAgeSecondsClampsUnknownAndFutureTimes(t *testing.T) {
	if got := resourceAgeSeconds(metav1.Time{}); got != 0 {
		t.Fatalf("zero resource age = %d", got)
	}
	if got := resourceAgeSeconds(metav1.NewTime(time.Now().Add(time.Hour))); got != 0 {
		t.Fatalf("future resource age = %d", got)
	}
	if got := resourceAgeSeconds(metav1.NewTime(time.Now().Add(-time.Minute))); got < 59 || got > 61 {
		t.Fatalf("one-minute resource age = %d", got)
	}
}

func TestMapKubernetesErrorUsesStablePublicCategories(t *testing.T) {
	resource := schema.GroupResource{Group: "", Resource: "pods"}
	tests := []struct {
		name string
		err  error
		code controlplaneapi.ErrorCode
	}{
		{name: "not found", err: apierrors.NewNotFound(resource, "api-0"), code: controlplaneapi.CodeNotFound},
		{
			name: "forbidden",
			err:  apierrors.NewForbidden(resource, "api-0", errors.New("rbac detail")),
			code: controlplaneapi.CodeForbidden,
		},
		{name: "rate limited", err: apierrors.NewTooManyRequests("busy", 1), code: controlplaneapi.CodeRateLimited},
		{name: "unauthorized", err: apierrors.NewUnauthorized("expired"), code: controlplaneapi.CodeUnavailable},
		{name: "timeout", err: context.DeadlineExceeded, code: controlplaneapi.CodeUnavailable},
		{name: "internal", err: errors.New("transport detail"), code: controlplaneapi.CodeInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiError := mapKubernetesError(test.err)
			if apiError.Code != test.code || !errors.Is(apiError.Cause, test.err) {
				t.Fatalf("mapped error = %#v", apiError)
			}
		})
	}
}
