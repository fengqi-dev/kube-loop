package relayregistry

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func TestEndpointHostPolicyAcceptsExactAndSubdomainOnly(t *testing.T) {
	policy, err := EndpointHostPolicy(
		"relay.example.com",
		".gateways.example.net",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"wss://relay.example.com/tunnel",
		"wss://pod-1.gateways.example.net/tunnel",
	} {
		if err := policy(relaycontrol.PeerIdentity{}, endpoint); err != nil {
			t.Fatalf("%s rejected: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"wss://example.com/tunnel",
		"wss://gateways.example.net/tunnel",
		"wss://pod-1.gateways.example.net.attacker.invalid/tunnel",
	} {
		if err := policy(relaycontrol.PeerIdentity{}, endpoint); err == nil {
			t.Fatalf("%s accepted", endpoint)
		}
	}
}

func TestKubernetesTopologyResolverBindsUIDServiceAccountAndNode(t *testing.T) {
	client := fake.NewSimpleClientset([]runtime.Object{
		&corev1.Pod{
			Name: "gateway-1", Namespace: "kubeloop", UID: types.UID("pod-uid"),
			Labels: map[string]string{
				"app.kubernetes.io/component": "data-plane",
			}, Spec: corev1.PodSpec{ServiceAccountName: "gateway", NodeName: "node-a"}},
		&corev1.Node{
			Name: "node-a",
			Labels: map[string]string{
				TopologyRegion: "cn", TopologyZone: "cn-a", TopologyHostname: "node-a",
			},
		},
	}...)
	resolver, err := KubernetesTopologyResolver(
		client,
		map[string]string{"app.kubernetes.io/component": "data-plane"},
	)
	if err != nil {
		t.Fatal(err)
	}
	topology, err := resolver(context.Background(), relaycontrol.PeerIdentity{
		TrustDomain: "cluster.local", Namespace: "kubeloop", ServiceAccount: "gateway", PodUID: "pod-uid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if topology[TopologyZone] != "cn-a" ||
		topology[TopologyHostname] != "node-a" {
		t.Fatalf("topology = %#v", topology)
	}
	if _, err := resolver(context.Background(), relaycontrol.PeerIdentity{
		TrustDomain: "cluster.local", Namespace: "kubeloop", ServiceAccount: "other", PodUID: "pod-uid",
	}); err == nil {
		t.Fatal("mismatched ServiceAccount accepted")
	}
	controlPlanePod := &corev1.Pod{
		Name:      "controlPlane",
		Namespace: "kubeloop",
		Spec:      corev1.PodSpec{NodeName: "node-a"},
	}
	if _, err := client.CoreV1().
		Pods("kubeloop").
		Create(context.Background(), controlPlanePod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	controlPlaneTopology, err := KubernetesPodTopology(
		context.Background(),
		client,
		"kubeloop",
		"controlPlane",
	)
	if err != nil || controlPlaneTopology[TopologyRegion] != "cn" {
		t.Fatalf("Control Plane topology = %#v, %v", controlPlaneTopology, err)
	}
}
