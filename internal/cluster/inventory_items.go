package cluster

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

func serviceInfoFromCore(service *corev1.Service) (ServiceInfo, bool) {
	if service == nil {
		return ServiceInfo{}, false
	}
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == "None" {
		return ServiceInfo{}, false
	}
	if strings.EqualFold(string(service.Spec.Type), "ExternalName") {
		return ServiceInfo{}, false
	}
	ports := make([]ServicePortInfo, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		protocol := string(port.Protocol)
		if protocol == "" {
			protocol = "TCP"
		}
		ports = append(ports, ServicePortInfo{
			Name: port.Name, Port: port.Port, Protocol: protocol,
		})
	}
	if len(ports) == 0 {
		return ServiceInfo{}, false
	}
	return ServiceInfo{
		Name:      service.Name,
		Namespace: service.Namespace,
		ClusterIP: service.Spec.ClusterIP,
		Ports:     ports,
	}, true
}

func podInfoFromCore(pod *corev1.Pod) (PodInfo, bool) {
	if pod == nil {
		return PodInfo{}, false
	}
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return PodInfo{}, false
	}
	return PodInfo{
		Name:       pod.Name,
		UID:        string(pod.UID),
		Namespace:  pod.Namespace,
		Phase:      string(pod.Status.Phase),
		Ready:      podReady(*pod),
		IP:         pod.Status.PodIP,
		Node:       pod.Spec.NodeName,
		Containers: collectPodContainers(*pod),
		Ports:      collectPodPorts(*pod),
	}, true
}

func serviceInfosFromList(services []*corev1.Service) []ServiceInfo {
	items := make([]ServiceInfo, 0, len(services))
	for _, service := range services {
		if info, ok := serviceInfoFromCore(service); ok {
			items = append(items, info)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func podInfosFromList(pods []*corev1.Pod) []PodInfo {
	items := make([]PodInfo, 0, len(pods))
	for _, pod := range pods {
		if info, ok := podInfoFromCore(pod); ok {
			items = append(items, info)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func ServiceHasPort(info ServiceInfo, port int32) bool {
	for _, item := range info.Ports {
		if item.Port == port {
			return true
		}
	}
	return false
}

func PodHasPort(info PodInfo, port uint16) bool {
	for _, item := range info.Ports {
		if item.Port == int32(port) {
			return true
		}
	}
	return false
}
