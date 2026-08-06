// Package gatewayruntime owns the Kubernetes resources used by the in-cluster
// gateway. It deliberately accepts a Kubernetes client so kubeconfig and
// context selection remain in the parent cluster provider.
package gatewayruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	Namespace = "kubeloop-system"
	Name      = "kubeloop-gateway"
	Port      = 1080

	localDevImage       = "kube-loop-gateway:dev"
	localDevImagePrefix = localDevImage + "-"
)

const defaultImage = "ghcr.io/fengqi-dev/kube-loop/gateway:latest"

var labels = map[string]string{
	"app.kubernetes.io/name":       Name,
	"app.kubernetes.io/part-of":    "kubeloop",
	"app.kubernetes.io/managed-by": "kubeloop",
}

// Info identifies a running gateway Pod.
type Info struct {
	Name string
	IP   string
}

// Ensure creates or updates the gateway resources and waits for a ready Pod.
func Ensure(ctx context.Context, client kubernetes.Interface, image string) (Info, error) {
	if image == "" {
		return Info{}, errors.New("gateway image is required")
	}
	if client == nil {
		return Info{}, errors.New("Kubernetes client is required")
	}
	if err := ensureNamespace(ctx, client); err != nil {
		return Info{}, err
	}
	if err := ensureDeployment(ctx, client, image); err != nil {
		return Info{}, err
	}
	return waitForPod(ctx, client, image)
}

// Find returns the first ready gateway Pod.
func Find(ctx context.Context, client kubernetes.Interface) (Info, error) {
	if client == nil {
		return Info{}, errors.New("Kubernetes client is required")
	}
	list, err := client.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + Name,
	})
	if err != nil {
		return Info{}, fmt.Errorf("list gateway pods: %w", err)
	}
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" || !podReady(pod) {
			continue
		}
		return Info{Name: pod.Name, IP: pod.Status.PodIP}, nil
	}
	return Info{}, errors.New("gateway pod not found; ask an admin to install kubeloop-gateway")
}

// InstallManifest returns resources an administrator can apply when the caller
// lacks permission to install the gateway.
func InstallManifest(image string) string {
	if image == "" {
		image = defaultImage
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/part-of: kubeloop
    app.kubernetes.io/managed-by: kubeloop
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/part-of: kubeloop
    app.kubernetes.io/managed-by: kubeloop
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/part-of: kubeloop
        app.kubernetes.io/managed-by: kubeloop
    spec:
      automountServiceAccountToken: false
      containers:
        - name: gateway
          image: %s
          ports:
            - name: tunnel
              containerPort: %d
              protocol: TCP
`, Namespace, Name, Name, Namespace, Name, Name, Name, image, Port)
}

func ensureNamespace(ctx context.Context, client kubernetes.Interface) error {
	_, err := client.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get gateway namespace: %w", err)
	}
	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: Namespace, Labels: labels},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create gateway namespace: %w", err)
	}
	return nil
}

func ensureDeployment(ctx context.Context, client kubernetes.Interface, image string) error {
	expected := deployment(image)
	existing, err := client.AppsV1().Deployments(Namespace).Get(ctx, Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = client.AppsV1().Deployments(Namespace).Create(
			ctx, expected, metav1.CreateOptions{},
		)
	case err != nil:
		return fmt.Errorf("get gateway deployment: %w", err)
	default:
		if existing.Labels["app.kubernetes.io/managed-by"] != "kubeloop" {
			return errors.New("gateway deployment exists but is not managed by kube-loop")
		}
		expected.ResourceVersion = existing.ResourceVersion
		_, err = client.AppsV1().Deployments(Namespace).Update(
			ctx, expected, metav1.UpdateOptions{},
		)
	}
	if err != nil {
		return fmt.Errorf("apply gateway deployment: %w", err)
	}
	return nil
}

func deployment(image string) *appsv1.Deployment {
	replicas := int32(1)
	runAsNonRoot := true
	allowPrivilegeEscalation := false
	readOnlyRootFilesystem := true
	pullPolicy := corev1.PullIfNotPresent
	if image == localDevImage || strings.HasPrefix(image, localDevImagePrefix) {
		pullPolicy = corev1.PullNever
	} else if strings.HasSuffix(image, ":latest") {
		pullPolicy = corev1.PullAlways
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: Name, Namespace: Namespace, Labels: labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": Name,
			}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: new(false),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   &runAsNonRoot,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{{
						Name:            "gateway",
						Image:           image,
						ImagePullPolicy: pullPolicy,
						Ports: []corev1.ContainerPort{{
							Name: "tunnel", ContainerPort: Port, Protocol: corev1.ProtocolTCP,
						}},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &allowPrivilegeEscalation,
							ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{
									Port: intstr.FromInt32(Port),
								},
							},
							InitialDelaySeconds: 1,
							PeriodSeconds:       2,
						},
					}},
				},
			},
		},
	}
}

func waitForPod(
	ctx context.Context,
	client kubernetes.Interface,
	image string,
) (Info, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		info, err := findPod(ctx, client, image)
		if err == nil {
			return info, nil
		}
		if apierrors.IsForbidden(err) {
			return Info{}, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return Info{}, fmt.Errorf("wait for gateway: %w", lastErr)
			}
			return Info{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func findPod(
	ctx context.Context,
	client kubernetes.Interface,
	image string,
) (Info, error) {
	list, err := client.CoreV1().Pods(Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + Name,
	})
	if err != nil {
		return Info{}, fmt.Errorf("list gateway pods: %w", err)
	}
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" || !podReady(pod) {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if container.Name == "gateway" && container.Image == image {
				return Info{Name: pod.Name, IP: pod.Status.PodIP}, nil
			}
		}
	}
	return Info{}, fmt.Errorf("gateway pod using image %q not found", image)
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
