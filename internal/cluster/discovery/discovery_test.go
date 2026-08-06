package discovery

import (
	"context"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCollectNodePodCIDRsUsesOnlyAdvertisedCIDRs(t *testing.T) {
	nodes := []corev1.Node{
		{
			Spec: corev1.NodeSpec{
				PodCIDR:  "10.244.0.7/24",
				PodCIDRs: []string{"10.244.0.0/24", "fd00:10:244::1/64"},
			},
		},
	}

	got := sortedKeys(collectNodePodCIDRs(nodes))
	want := []string{"10.244.0.0/24", "fd00:10:244::/64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectNodePodCIDRs() = %v, want %v", got, want)
	}
}

func TestDiscoverPreservesEmptyNetworkLists(t *testing.T) {
	result, err := Discover(context.Background(), fake.NewSimpleClientset(), nil)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	for name, values := range map[string][]string{
		"PodCIDRs":     result.PodCIDRs,
		"ServiceCIDRs": result.ServiceCIDRs,
		"ServiceIPs":   result.ServiceIPs,
	} {
		if values == nil {
			t.Errorf("%s = nil, want empty slice", name)
		}
	}
}

func TestDiscoverCollectsScopedNetworkInventory(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Node{Spec: corev1.NodeSpec{PodCIDR: "10.244.1.0/24"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ignored", Namespace: "other"}},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
			Spec:       corev1.ServiceSpec{ClusterIPs: []string{"10.96.1.10"}},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-dns", Namespace: "kube-system"},
			Spec:       corev1.ServiceSpec{ClusterIPs: []string{"10.96.0.10"}},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "kubeadm-config", Namespace: "kube-system"},
			Data: map[string]string{
				"ClusterConfiguration": "networking:\n  serviceSubnet: 10.96.0.0/12\n",
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		},
	)

	result, err := Discover(context.Background(), client, []string{"default"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if result.Pods != 1 || result.Services != 2 || result.Deployments != 1 {
		t.Fatalf("inventory = %+v", result)
	}
	if result.DNSServer != "10.96.0.10" {
		t.Fatalf("DNS server = %q", result.DNSServer)
	}
	if !reflect.DeepEqual(result.PodCIDRs, []string{"10.244.1.0/24"}) {
		t.Fatalf("Pod CIDRs = %v", result.PodCIDRs)
	}
	if !reflect.DeepEqual(result.ServiceCIDRs, []string{"10.96.0.0/12"}) {
		t.Fatalf("Service CIDRs = %v", result.ServiceCIDRs)
	}
	if !reflect.DeepEqual(result.ServiceIPs, []string{"10.96.0.10", "10.96.1.10"}) {
		t.Fatalf("Service IPs = %v", result.ServiceIPs)
	}
}
