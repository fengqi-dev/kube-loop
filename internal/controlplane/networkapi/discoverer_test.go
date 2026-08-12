package networkapi

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type fakeProvider struct {
	client       kubernetes.Interface
	systemClient kubernetes.Interface
	subject      authorization.Subject
	systemCalls  int
}

func (provider *fakeProvider) ClientFor(subject authorization.Subject) (kubernetes.Interface, error) {
	provider.subject = subject
	return provider.client, nil
}

func (provider *fakeProvider) SystemClient() (kubernetes.Interface, error) {
	provider.systemCalls++
	return provider.systemClient, nil
}

func TestDiscoverUsesPrincipalClientAndReturnsNormalizedSpec(t *testing.T) {
	provider := &fakeProvider{
		client: fake.NewClientset(
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "development"}, Status: corev1.PodStatus{PodIP: "10.2.1.9"}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "development"}, Spec: corev1.ServiceSpec{ClusterIP: "10.96.1.20", ClusterIPs: []string{"10.96.1.20"}}},
		),
		systemClient: fake.NewClientset(
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "kube-dns", Namespace: "kube-system"}, Spec: corev1.ServiceSpec{ClusterIP: "10.96.0.10", ClusterIPs: []string{"10.96.0.10"}}},
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"}, Data: map[string]string{
				"Corefile": ".:53 {\n  kubernetes corp.internal in-addr.arpa ip6.arpa {\n  }\n}",
			}},
		),
	}
	discoverer, err := NewDiscoverer(provider)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := discoverer.Discover(context.Background(), controlplaneapi.Principal{
		Subject: "principal-a", Groups: []string{"developers"},
	}, "development")
	if err != nil {
		t.Fatal(err)
	}
	if provider.subject.ID != "principal-a" || len(provider.subject.Groups) != 1 || provider.systemCalls != 1 ||
		len(spec.PodCIDRs) == 0 || !slices.Equal(spec.PodIPs, []string{"10.2.1.9"}) ||
		len(spec.ServiceCIDRs) == 0 || spec.DNSServer != "10.96.0.10" ||
		!slices.Equal(spec.ClusterDomains, []string{"cluster.local", "corp.internal"}) {
		t.Fatalf("subject=%#v spec=%#v", provider.subject, spec)
	}
}

func TestParseCoreDNSClusterDomainsIsBoundedAndStrict(t *testing.T) {
	corefile := `.:53 {
  kubernetes DEV.Internal. in-addr.arpa ip6.arpa {
  }
  # kubernetes ignored.example
  forward . /etc/resolv.conf
}
example.org:53 {
  kubernetes dev.internal valid.example bad_domain {
  }
}`
	if got, want := parseCoreDNSClusterDomains(corefile), []string{"dev.internal", "valid.example"}; !slices.Equal(got, want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	if got := parseCoreDNSClusterDomains(strings.Repeat("a", maximumCorefileBytes+1)); got != nil {
		t.Fatalf("oversized Corefile domains = %v", got)
	}
}
