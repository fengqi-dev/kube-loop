package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// PodPortInfo describes one container port exposed by a Pod.
type PodPortInfo struct {
	Name     string `json:"name"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

// PodInfo is a running Pod shown in the network and port-forward UI.
type PodInfo struct {
	Name       string        `json:"name"`
	Namespace  string        `json:"namespace"`
	Phase      string        `json:"phase"`
	Ready      bool          `json:"ready"`
	IP         string        `json:"ip,omitempty"`
	Node       string        `json:"node,omitempty"`
	Containers []string      `json:"containers"`
	Ports      []PodPortInfo `json:"ports"`
}

func (p *Provider) ListPods(
	ctx context.Context, contextName, namespace string,
) ([]PodInfo, error) {
	listNS := apiNamespace(namespace)
	client, err := p.client(contextName)
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Pods(listNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	refs := make([]*corev1.Pod, 0, len(list.Items))
	for i := range list.Items {
		refs = append(refs, &list.Items[i])
	}
	return podInfosFromList(refs), nil
}

// ResolveServiceBackend picks a ready Pod behind a Service and the container port.
func (p *Provider) ResolveServiceBackend(
	ctx context.Context, contextName, namespace, serviceName string, servicePort int32,
) (podName string, targetPort uint16, err error) {
	client, err := p.client(contextName)
	if err != nil {
		return "", 0, err
	}
	service, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("get service: %w", err)
	}
	var matched *corev1.ServicePort
	for i := range service.Spec.Ports {
		if service.Spec.Ports[i].Port == servicePort {
			matched = &service.Spec.Ports[i]
			break
		}
	}
	if matched == nil {
		return "", 0, fmt.Errorf("service port %d not found", servicePort)
	}

	pod, err := pickServicePod(ctx, client, namespace, service)
	if err != nil {
		return "", 0, err
	}
	port, err := resolveTargetPort(*pod, *matched)
	if err != nil {
		return "", 0, err
	}
	return pod.Name, port, nil
}

func pickServicePod(
	ctx context.Context, client kubernetes.Interface, namespace string, service *corev1.Service,
) (*corev1.Pod, error) {
	if len(service.Spec.Selector) > 0 {
		list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labels.Set(service.Spec.Selector).String(),
		})
		if err != nil {
			return nil, fmt.Errorf("list service pods: %w", err)
		}
		for i := range list.Items {
			pod := &list.Items[i]
			if pod.DeletionTimestamp != nil {
				continue
			}
			if pod.Status.Phase == corev1.PodRunning && podReady(*pod) {
				return pod, nil
			}
		}
		return nil, fmt.Errorf("no ready pods for service %s/%s", namespace, service.Name)
	}

	slices, err := client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: interceptServiceNameLabel + "=" + service.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("list endpoint slices: %w", err)
	}
	for _, slice := range slices.Items {
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			if endpoint.TargetRef != nil && endpoint.TargetRef.Kind == "Pod" && endpoint.TargetRef.Name != "" {
				pod, getErr := client.CoreV1().Pods(namespace).Get(ctx, endpoint.TargetRef.Name, metav1.GetOptions{})
				if getErr != nil {
					continue
				}
				if pod.Status.Phase == corev1.PodRunning && podReady(*pod) {
					return pod, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no ready pods for service %s/%s", namespace, service.Name)
}

func resolveTargetPort(pod corev1.Pod, servicePort corev1.ServicePort) (uint16, error) {
	switch servicePort.TargetPort.Type {
	case intstr.Int:
		if servicePort.TargetPort.IntVal > 0 {
			return uint16(servicePort.TargetPort.IntVal), nil
		}
		return uint16(servicePort.Port), nil
	case intstr.String:
		name := servicePort.TargetPort.StrVal
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == name {
					return uint16(port.ContainerPort), nil
				}
			}
		}
		return 0, fmt.Errorf("named target port %q not found on pod %s", name, pod.Name)
	default:
		return uint16(servicePort.Port), nil
	}
}

func collectPodPorts(pod corev1.Pod) []PodPortInfo {
	seen := map[string]struct{}{}
	ports := make([]PodPortInfo, 0)
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			protocol := string(port.Protocol)
			if protocol == "" {
				protocol = "TCP"
			}
			key := fmt.Sprintf("%s:%d", protocol, port.ContainerPort)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ports = append(ports, PodPortInfo{
				Name: port.Name, Port: port.ContainerPort, Protocol: protocol,
			})
		}
	}
	return ports
}

func collectPodContainers(pod corev1.Pod) []string {
	containers := make([]string, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		if container.Name != "" {
			containers = append(containers, container.Name)
		}
	}
	return containers
}
