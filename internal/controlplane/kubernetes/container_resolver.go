package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ContainerResolver struct {
	provider ClientProvider
}

func NewContainerResolver(provider ClientProvider) (*ContainerResolver, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes client Provider is required")
	}
	return &ContainerResolver{provider: provider}, nil
}

func (r *ContainerResolver) ResolveContainer(
	ctx context.Context,
	principal controlplaneapi.Principal,
	namespace string,
	podName string,
	containerName string,
) (string, error) {
	client, err := r.provider.ClientFor(subjectFor(principal))
	if err != nil {
		return "", err
	}
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return "", fmt.Errorf("Pod %s/%s is not running", namespace, podName)
	}
	containers := make([]string, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers)+len(pod.Spec.EphemeralContainers))
	for _, container := range pod.Spec.InitContainers {
		containers = append(containers, container.Name)
	}
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
	}
	for _, container := range pod.Spec.EphemeralContainers {
		containers = append(containers, container.Name)
	}
	if containerName == "" {
		if len(containers) != 1 {
			return "", errors.New("container is required when the Pod has multiple containers")
		}
		return containers[0], nil
	}
	if !slices.Contains(containers, containerName) {
		return "", fmt.Errorf("container %q does not exist in Pod %s/%s", containerName, namespace, podName)
	}
	return containerName, nil
}
