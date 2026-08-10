package portforwardapi_test

import (
	"context"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/portforwardapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type staticClientProvider struct{ client kubernetes.Interface }

func (provider staticClientProvider) ClientFor(authorization.Subject) (kubernetes.Interface, error) {
	return provider.client, nil
}

func TestKubernetesResolverResolvesPodAndServiceInsideGateway(t *testing.T) {
	objects := []runtime.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "development"}, Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.244.1.7"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "development"}, Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.20", Ports: []corev1.ServicePort{{Port: 8443, Protocol: corev1.ProtocolTCP}},
		}},
	}
	resolver, err := portforwardapi.NewKubernetesResolver(staticClientProvider{client: fake.NewSimpleClientset(objects...)})
	if err != nil {
		t.Fatal(err)
	}
	principal := controller.Principal{Subject: "user"}
	pod, err := resolver.Resolve(context.Background(), principal, "development", portforwardapi.Spec{
		Kind: "pod", Name: "api-0", Protocol: "tcp", RemotePort: 8080,
	})
	if err != nil || pod.Address() != "10.244.1.7:8080" {
		t.Fatalf("pod target = %#v err = %v", pod, err)
	}
	service, err := resolver.Resolve(context.Background(), principal, "development", portforwardapi.Spec{
		Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
	})
	if err != nil || service.Address() != "10.96.0.20:8443" {
		t.Fatalf("service target = %#v err = %v", service, err)
	}
	if _, err := resolver.Resolve(context.Background(), principal, "development", portforwardapi.Spec{
		Kind: "service", Name: "api", Protocol: "udp", RemotePort: 8443,
	}); err == nil {
		t.Fatal("expected protocol mismatch to fail")
	}
}
