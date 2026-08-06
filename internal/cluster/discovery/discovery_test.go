package discovery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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
				"ClusterConfiguration": "networking:\n" +
					"  podSubnet: 10.244.0.0/16\n" +
					"  serviceSubnet: 10.96.0.0/12\n",
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
	if !reflect.DeepEqual(result.PodCIDRs, []string{"10.244.0.0/16"}) {
		t.Fatalf("Pod CIDRs = %v", result.PodCIDRs)
	}
	if !reflect.DeepEqual(result.ServiceCIDRs, []string{"10.96.0.0/12"}) {
		t.Fatalf("Service CIDRs = %v", result.ServiceCIDRs)
	}
	if !reflect.DeepEqual(result.ServiceIPs, []string{"10.96.0.10", "10.96.1.10"}) {
		t.Fatalf("Service IPs = %v", result.ServiceIPs)
	}
}

func TestDiscoverFallsBackToObservedIPsAndComponentFlags(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "controller", Namespace: "kube-system"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Command: []string{"kube-controller-manager"},
				Args:    []string{"--cluster-cidr=10.244.0.0/16"},
			}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "kube-system"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Args: []string{"--service-cluster-ip-range", "10.96.0.0/12"},
			}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
			Status: corev1.PodStatus{
				PodIP:  "10.244.3.125",
				PodIPs: []corev1.PodIP{{IP: "fd00:10:244::125"}},
			},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP:  "10.96.1.10",
				ClusterIPs: []string{"10.96.1.10", "fd00:10:96::10"},
			},
		},
	)

	result, err := Discover(context.Background(), client, nil)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !reflect.DeepEqual(result.PodCIDRs, []string{"10.244.0.0/16", "fd00:10:244::/64"}) {
		t.Fatalf("Pod CIDRs = %v", result.PodCIDRs)
	}
	if !reflect.DeepEqual(result.ServiceCIDRs, []string{"10.96.0.0/12", "fd00:10:96::/64"}) {
		t.Fatalf("Service CIDRs = %v", result.ServiceCIDRs)
	}
}

func TestCompactCIDRsDropsCoveredRanges(t *testing.T) {
	got := compactCIDRs(map[string]struct{}{
		"10.244.0.0/16":      {},
		"10.244.0.0/24":      {},
		"10.244.3.125/32":    {},
		"fd00:10:244::/64":   {},
		"fd00:10:244::1/128": {},
	})
	want := []string{"10.244.0.0/16", "fd00:10:244::/64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compactCIDRs() = %v, want %v", got, want)
	}
}

func TestProbeServiceCIDRsParsesRejectedDryRun(t *testing.T) {
	client := fake.NewSimpleClientset()
	createCalls := 0
	client.PrependReactor("create", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		createCalls++
		return true, nil, errors.New(
			`Service is invalid: the provided IP is not in the valid range. ` +
				`The range of valid IPs is 10.96.0.0/12`,
		)
	})

	got := compactCIDRs(probeServiceCIDRs(context.Background(), client, "default"))
	if !reflect.DeepEqual(got, []string{"10.96.0.0/12"}) {
		t.Fatalf("probeServiceCIDRs() = %v", got)
	}
	if createCalls != 2 {
		t.Fatalf("create calls = %d, want IPv4 and IPv6 probes", createCalls)
	}
}

func TestServiceCIDRsFromErrorRejectsUnrelatedCIDRs(t *testing.T) {
	got := serviceCIDRsFromError(
		`request from 192.0.2.1/32 failed: The range of valid IPs is ` +
			`10.96.0.0/12, fd00:10:96::/108`,
	)
	want := map[string]struct{}{
		"10.96.0.0/12":     {},
		"fd00:10:96::/108": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("serviceCIDRsFromError() = %v, want %v", got, want)
	}
	if got := serviceCIDRsFromError("unrelated 192.0.2.0/24"); len(got) != 0 {
		t.Fatalf("unrelated error yielded %v", got)
	}
}
