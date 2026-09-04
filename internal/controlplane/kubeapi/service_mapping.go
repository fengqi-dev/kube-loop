package kubeapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func podFromKubernetes(pod *corev1.Pod) podDocument {
	containers := make([]string, 0, len(pod.Spec.Containers))
	ports := make([]podPortDocument, 0)
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
		for _, port := range container.Ports {
			ports = append(ports, podPortDocument{
				Name: port.Name, Port: port.ContainerPort, Protocol: string(port.Protocol),
			})
		}
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady &&
			condition.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	var readyContainers, restarts int32
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {
			readyContainers++
		}
		restarts += status.RestartCount
	}
	totalContainers := min(len(pod.Spec.Containers), math.MaxInt32)
	return podDocument{
		Name: pod.Name, Namespace: pod.Namespace, Phase: string(pod.Status.Phase),
		PodIP: pod.Status.PodIP, NodeName: pod.Spec.NodeName, Ready: ready,
		ReadyContainers: readyContainers,
		TotalContainers: int32(totalContainers), Restarts: restarts,
		AgeSeconds: resourceAgeSeconds(
			pod.CreationTimestamp,
		), Containers: containers, Ports: ports,
	}
}

func serviceFromKubernetes(service *corev1.Service) serviceDocument {
	ports := make([]servicePortDocument, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports = append(ports, servicePortDocument{
			Name: port.Name, Port: port.Port, Protocol: string(port.Protocol), TargetPort: port.TargetPort.String(),
		})
	}
	externalIPs := append([]string(nil), service.Spec.ExternalIPs...)
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			externalIPs = append(externalIPs, ingress.IP)
		} else if ingress.Hostname != "" {
			externalIPs = append(externalIPs, ingress.Hostname)
		}
	}
	if service.Spec.ExternalName != "" {
		externalIPs = append(externalIPs, service.Spec.ExternalName)
	}
	return serviceDocument{
		Name: service.Name, Namespace: service.Namespace, Type: string(service.Spec.Type),
		ClusterIP: service.Spec.ClusterIP, ExternalName: service.Spec.ExternalName,
		ExternalIPs: externalIPs, AgeSeconds: resourceAgeSeconds(service.CreationTimestamp), Ports: ports,
	}
}

func resourceAgeSeconds(created metav1.Time) int64 {
	if created.Time.IsZero() {
		return 0
	}
	age := int64(time.Since(created.Time).Seconds())
	if age < 0 {
		return 0
	}
	return age
}

func mapKubernetesError(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsNotFound(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeNotFound,
			Message: "resource not found",
			Cause:   err,
		}
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "Kubernetes operation is not permitted",
			Cause:   err,
		}
	case apierrors.IsTooManyRequests(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeRateLimited,
			Message: "Kubernetes API rate limit exceeded",
			Cause:   err,
		}
	case isUnavailableKubernetesError(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: kubernetesAPIUnavailableMessage,
			Cause:   err,
		}
	default:
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: "Kubernetes operation failed",
			Cause:   err,
		}
	}
}

func isUnavailableKubernetesError(err error) bool {
	return apierrors.IsUnauthorized(err) || apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func writeJSON(ctx *echo.Context, value any) {
	_ = ctx.JSON(http.StatusOK, value)
}
