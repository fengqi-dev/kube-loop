package relayregistry

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

const (
	TopologyRegion   = "topology.kubernetes.io/region"
	TopologyZone     = "topology.kubernetes.io/zone"
	TopologyHostname = "kubernetes.io/hostname"
)

func KubernetesTopologyResolver(
	client kubernetes.Interface,
	labelSelector map[string]string,
) (TopologyResolver, error) {
	if client == nil {
		return nil, errors.New(
			"kubernetes client is required for Relay topology",
		)
	}
	selector := labels.SelectorFromSet(labelSelector).String()
	return func(ctx context.Context, identity relaycontrol.PeerIdentity) (map[string]string, error) {
		pods, err := client.CoreV1().
			Pods(identity.Namespace).
			List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("list Data Plane Pods: %w", err)
		}
		for index := range pods.Items {
			pod := &pods.Items[index]
			if string(pod.UID) != identity.PodUID {
				continue
			}
			if pod.Spec.ServiceAccountName != identity.ServiceAccount ||
				pod.Spec.NodeName == "" {
				return nil, errors.New(
					"authenticated Data Plane Pod identity does not match Kubernetes",
				)
			}
			return nodeTopology(ctx, client, pod.Spec.NodeName)
		}
		return nil, errors.New("authenticated Data Plane Pod was not found")
	}, nil
}

func KubernetesPodTopology(
	ctx context.Context,
	client kubernetes.Interface,
	namespace, podName string,
) (map[string]string, error) {
	if client == nil || namespace == "" || podName == "" {
		return nil, errors.New(
			"control plane Pod identity is required for Relay topology",
		)
	}
	pod, err := client.CoreV1().
		Pods(namespace).
		Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Control Plane Pod: %w", err)
	}
	if pod.Spec.NodeName == "" {
		return nil, errors.New("control plane Pod is not scheduled")
	}
	return nodeTopology(ctx, client, pod.Spec.NodeName)
}

func nodeTopology(
	ctx context.Context,
	client kubernetes.Interface,
	nodeName string,
) (map[string]string, error) {
	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get topology Node: %w", err)
	}
	topology := make(map[string]string, 3)
	for _, key := range []string{TopologyRegion, TopologyZone, TopologyHostname} {
		if value := node.Labels[key]; value != "" {
			topology[key] = value
		}
	}
	return topology, nil
}
