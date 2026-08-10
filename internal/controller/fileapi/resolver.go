package fileapi

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type KubernetesProvider interface {
	ClientFor(authorization.Subject) (kubernetes.Interface, error)
}

type KubernetesTargetResolver struct{ provider KubernetesProvider }

func NewKubernetesTargetResolver(provider KubernetesProvider) (*KubernetesTargetResolver, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes Provider is required")
	}
	return &KubernetesTargetResolver{provider: provider}, nil
}

func (resolver *KubernetesTargetResolver) ResolveContainer(
	ctx context.Context,
	principal controller.Principal,
	namespace, podName, containerName string,
) (string, error) {
	client, err := resolver.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
	})
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

var _ TargetResolver = (*KubernetesTargetResolver)(nil)
