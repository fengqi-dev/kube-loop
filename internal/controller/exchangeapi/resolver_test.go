package exchangeapi

import (
	"context"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type exchangeTestProvider struct{ client kubernetes.Interface }

func (provider exchangeTestProvider) ClientFor(authorization.Subject) (kubernetes.Interface, error) {
	return provider.client, nil
}

func TestKubernetesResolverRequiresEveryRequestedPortToBeAuthoritative(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "development"},
		Spec: corev1.ServiceSpec{
			ClusterIP: "10.96.0.20",
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
				{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP},
			},
		},
	})
	resolver, err := NewKubernetesServiceResolver(exchangeTestProvider{client: client})
	if err != nil {
		t.Fatal(err)
	}
	service, err := resolver.ResolveService(
		context.Background(), controller.Principal{Subject: "user"}, "development", "api",
		[]Port{{ServicePort: 53, Protocol: "udp"}, {ServicePort: 80, Protocol: "tcp"}},
	)
	if err != nil || service.ClusterIP != "10.96.0.20" || len(service.Ports) != 2 || service.Ports[0].Name != "dns" {
		t.Fatalf("resolved Service=%#v err=%v", service, err)
	}
	partial, err := resolver.ResolveService(
		context.Background(), controller.Principal{Subject: "user"}, "development", "api",
		[]Port{{ServicePort: 80, Protocol: "tcp"}},
	)
	if err != nil || len(partial.Ports) != 1 || partial.Ports[0].Name != "http" {
		t.Fatalf("selected Exchange port=%#v err=%v", partial, err)
	}
	if _, err := resolver.ResolveService(
		context.Background(), controller.Principal{Subject: "user"}, "development", "api",
		[]Port{{ServicePort: 53, Protocol: "tcp"}, {ServicePort: 80, Protocol: "tcp"}},
	); err == nil {
		t.Fatal("mismatched Exchange protocol was accepted")
	}
}
