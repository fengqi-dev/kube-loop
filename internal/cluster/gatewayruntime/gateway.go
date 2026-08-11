// Package gatewayruntime owns the Kubernetes resources used by the in-cluster
// gateway. It deliberately accepts a Kubernetes client so kubeconfig and
// context selection remain in the parent cluster provider.
package gatewayruntime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	Namespace = "kubeloop-system"
	Name      = "kubeloop-gateway"
	Port      = 1080
	HTTPPort  = 8080
	TokenKey  = "token"

	localDevImage       = "kube-loop-gateway:dev"
	localDevImagePrefix = localDevImage + "-"
)

const defaultImage = "ghcr.io/fengqi-dev/kube-loop/gateway:latest"

func labels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/part-of":    "kubeloop",
		"app.kubernetes.io/managed-by": "kubeloop",
	}
}

// EnsureHTTPResource enables the authenticated WebSocket listener and creates
// the stable ClusterIP Service that an Ingress should target.
func EnsureHTTPResource(
	ctx context.Context,
	client kubernetes.Interface,
	image, namespace, name, token, endpoint string,
) (Info, error) {
	if strings.TrimSpace(token) == "" {
		return Info{}, errors.New("Gateway HTTP token is required")
	}
	if image == "" || namespace == "" || name == "" || client == nil {
		return Info{}, errors.New("Gateway image, namespace, name, and Kubernetes client are required")
	}
	if err := ensureNamespace(ctx, client, namespace); err != nil {
		return Info{}, err
	}
	if err := ensureTokenSecret(ctx, client, namespace, name, token); err != nil {
		return Info{}, err
	}
	if err := ensureHTTPDeployment(ctx, client, image, namespace, name); err != nil {
		return Info{}, err
	}
	if err := ensureHTTPService(ctx, client, namespace, name); err != nil {
		return Info{}, err
	}
	if err := ensureHTTPIngress(ctx, client, namespace, name, endpoint); err != nil {
		return Info{}, err
	}
	return waitForPod(ctx, client, image, namespace, name)
}

// Info identifies a running gateway Pod.
type Info struct {
	Name string
	IP   string
}

// Ensure creates or updates the gateway resources and waits for a ready Pod.
func Ensure(ctx context.Context, client kubernetes.Interface, image string) (Info, error) {
	return EnsureResource(ctx, client, image, Namespace, Name)
}

// EnsureResource creates or updates a Gateway in the supplied namespace.
func EnsureResource(
	ctx context.Context, client kubernetes.Interface, image, namespace, name string,
) (Info, error) {
	if image == "" {
		return Info{}, errors.New("gateway image is required")
	}
	if namespace == "" {
		return Info{}, errors.New("gateway namespace is required")
	}
	if name == "" {
		return Info{}, errors.New("gateway name is required")
	}
	if client == nil {
		return Info{}, errors.New("Kubernetes client is required")
	}
	if err := ensureNamespace(ctx, client, namespace); err != nil {
		return Info{}, err
	}
	if err := ensureDeployment(ctx, client, image, namespace, name); err != nil {
		return Info{}, err
	}
	return waitForPod(ctx, client, image, namespace, name)
}

// Find returns the first ready gateway Pod.
func Find(ctx context.Context, client kubernetes.Interface) (Info, error) {
	return FindResource(ctx, client, Namespace, Name)
}

func FindNamed(ctx context.Context, client kubernetes.Interface, name string) (Info, error) {
	return FindResource(ctx, client, Namespace, name)
}

func FindResource(
	ctx context.Context, client kubernetes.Interface, namespace, name string,
) (Info, error) {
	if client == nil {
		return Info{}, errors.New("Kubernetes client is required")
	}
	if namespace == "" {
		return Info{}, errors.New("gateway namespace is required")
	}
	if name == "" {
		return Info{}, errors.New("gateway name is required")
	}
	list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + name,
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
	return InstallManifestResource(image, Namespace, Name)
}

func InstallManifestNamed(image, name string) string {
	return InstallManifestResource(image, Namespace, name)
}

func InstallManifestResource(image, namespace, name string) string {
	if image == "" {
		image = defaultImage
	}
	if namespace == "" {
		namespace = Namespace
	}
	if name == "" {
		name = Name
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
`, namespace, nameForNamespace(namespace), name, namespace, name, name, name, image, Port)
}

func HTTPInstallManifestResource(image, namespace, name, token string) string {
	base := InstallManifestResource(image, namespace, name)
	base = strings.Replace(base,
		fmt.Sprintf("            - name: tunnel\n              containerPort: %d\n              protocol: TCP\n", Port),
		fmt.Sprintf("            - name: tunnel\n              containerPort: %d\n              protocol: TCP\n            - name: http\n              containerPort: %d\n              protocol: TCP\n          env:\n            - name: KUBELOOP_GATEWAY_TOKEN\n              valueFrom:\n                secretKeyRef:\n                  name: %s-http\n                  key: %s\n", Port, HTTPPort, name, TokenKey),
		1,
	)
	const deploymentMarker = "---\napiVersion: apps/v1"
	parts := strings.SplitN(base, deploymentMarker, 2)
	if len(parts) != 2 {
		return base
	}
	return fmt.Sprintf(`%s---
apiVersion: v1
kind: Secret
metadata:
  name: %s-http
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/part-of: kubeloop
    app.kubernetes.io/managed-by: kubeloop
type: Opaque
data:
  %s: %s
---
apiVersion: apps/v1%s---
apiVersion: v1
kind: Service
metadata:
  name: %s-http
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/part-of: kubeloop
    app.kubernetes.io/managed-by: kubeloop
spec:
  selector:
    app.kubernetes.io/name: %s
  ports:
    - name: http
      port: %d
      targetPort: http
      protocol: TCP
`, parts[0], name, namespace, name, TokenKey,
		base64.StdEncoding.EncodeToString([]byte(token)), parts[1],
		name, namespace, name, name, HTTPPort)
}

func HTTPIngressManifestResource(image, namespace, name, token, endpoint string) string {
	manifest := HTTPInstallManifestResource(image, namespace, name, token)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || isLoopbackHost(parsed.Hostname()) {
		return manifest
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/v1/tunnel"
	}
	rule := "    - http:\n"
	if net.ParseIP(parsed.Hostname()) == nil {
		rule = "    - host: " + parsed.Hostname() + "\n      http:\n"
	}
	return fmt.Sprintf(`%s---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s-http
  namespace: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/part-of: kubeloop
    app.kubernetes.io/managed-by: kubeloop
spec:
  rules:
%s
        paths:
          - path: %s
            pathType: Prefix
            backend:
              service:
                name: %s-http
                port:
                  name: http
`, manifest, name, namespace, name, strings.TrimSuffix(rule, "\n"), path, name)
}

func ensureNamespace(ctx context.Context, client kubernetes.Interface, namespace string) error {
	_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get gateway namespace: %w", err)
	}
	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: labels(nameForNamespace(namespace))},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create gateway namespace: %w", err)
	}
	return nil
}

func nameForNamespace(namespace string) string {
	if namespace == Namespace {
		return Name
	}
	return namespace
}

func ensureDeployment(
	ctx context.Context, client kubernetes.Interface, image, namespace, name string,
) error {
	expected := namedDeployment(image, namespace, name)
	existing, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = client.AppsV1().Deployments(namespace).Create(
			ctx, expected, metav1.CreateOptions{},
		)
	case err != nil:
		return fmt.Errorf("get gateway deployment: %w", err)
	default:
		if existing.Labels["app.kubernetes.io/managed-by"] != "kubeloop" {
			return errors.New("gateway deployment exists but is not managed by kube-loop")
		}
		expected.ResourceVersion = existing.ResourceVersion
		_, err = client.AppsV1().Deployments(namespace).Update(
			ctx, expected, metav1.UpdateOptions{},
		)
	}
	if err != nil {
		return fmt.Errorf("apply gateway deployment: %w", err)
	}
	return nil
}

func ensureHTTPDeployment(
	ctx context.Context, client kubernetes.Interface, image, namespace, name string,
) error {
	expected := namedDeployment(image, namespace, name)
	container := &expected.Spec.Template.Spec.Containers[0]
	container.Ports = append(container.Ports, corev1.ContainerPort{
		Name: "http", ContainerPort: HTTPPort, Protocol: corev1.ProtocolTCP,
	})
	container.Env = []corev1.EnvVar{{
		Name: "KUBELOOP_GATEWAY_TOKEN",
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name + "-http"},
			Key:                  TokenKey,
		}},
	}}
	existing, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = client.AppsV1().Deployments(namespace).Create(ctx, expected, metav1.CreateOptions{})
	case err != nil:
		return fmt.Errorf("get gateway deployment: %w", err)
	default:
		if existing.Labels["app.kubernetes.io/managed-by"] != "kubeloop" {
			return errors.New("gateway deployment exists but is not managed by kube-loop")
		}
		expected.ResourceVersion = existing.ResourceVersion
		_, err = client.AppsV1().Deployments(namespace).Update(ctx, expected, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply HTTP gateway deployment: %w", err)
	}
	return nil
}

func ensureTokenSecret(
	ctx context.Context, client kubernetes.Interface, namespace, name, token string,
) error {
	secretName := name + "-http"
	existing, err := client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace, Labels: labels(name)},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{TokenKey: token},
		}, metav1.CreateOptions{})
	} else if err == nil {
		if existing.Labels["app.kubernetes.io/managed-by"] != "kubeloop" {
			return errors.New("Gateway HTTP Secret exists but is not managed by kube-loop")
		}
		currentToken := string(existing.Data[TokenKey])
		if currentToken == "" {
			currentToken = existing.StringData[TokenKey]
		}
		if currentToken != token {
			return errors.New("Gateway HTTP token differs from the installed Secret; use the shared token or replace the Secret explicitly")
		}
	}
	if err != nil {
		return fmt.Errorf("apply Gateway HTTP Secret: %w", err)
	}
	return nil
}

func ensureHTTPService(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	serviceName := name + "-http"
	existing, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: namespace, Labels: labels(name)},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app.kubernetes.io/name": name},
				Ports:    []corev1.ServicePort{{Name: "http", Port: HTTPPort, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}},
			},
		}, metav1.CreateOptions{})
	} else if err == nil {
		if existing.Labels["app.kubernetes.io/managed-by"] != "kubeloop" {
			return errors.New("Gateway HTTP Service exists but is not managed by kube-loop")
		}
		existing.Labels = labels(name)
		existing.Spec.Selector = map[string]string{"app.kubernetes.io/name": name}
		existing.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: HTTPPort, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP}}
		_, err = client.CoreV1().Services(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply Gateway HTTP Service: %w", err)
	}
	return nil
}

func ensureHTTPIngress(
	ctx context.Context, client kubernetes.Interface, namespace, name, endpoint string,
) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Hostname() == "" {
		return errors.New("Gateway WebSocket endpoint must be an absolute ws:// or wss:// URL")
	}
	if isLoopbackHost(parsed.Hostname()) {
		return nil
	}

	path := parsed.Path
	if path == "" {
		path = "/v1/tunnel"
	}
	pathType := networkingv1.PathTypePrefix
	expected := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "-http", Namespace: namespace, Labels: labels(name),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: preferredIngressClass(ctx, client),
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
						Path: path, PathType: &pathType,
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: name + "-http",
							Port: networkingv1.ServiceBackendPort{Name: "http"},
						}},
					}}},
				},
			}},
		},
	}
	if net.ParseIP(parsed.Hostname()) == nil {
		expected.Spec.Rules[0].Host = parsed.Hostname()
	}
	if parsed.Scheme == "wss" && expected.Spec.Rules[0].Host != "" {
		expected.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{expected.Spec.Rules[0].Host}}}
	}

	ingresses := client.NetworkingV1().Ingresses(namespace)
	existing, err := ingresses.Get(ctx, expected.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = ingresses.Create(ctx, expected, metav1.CreateOptions{})
	case err != nil:
		return fmt.Errorf("get Gateway HTTP Ingress: %w", err)
	default:
		if existing.Labels["app.kubernetes.io/managed-by"] != "kubeloop" {
			return errors.New("Gateway HTTP Ingress exists but is not managed by kube-loop")
		}
		expected.ResourceVersion = existing.ResourceVersion
		_, err = ingresses.Update(ctx, expected, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply Gateway HTTP Ingress: %w", err)
	}
	return nil
}

func preferredIngressClass(ctx context.Context, client kubernetes.Interface) *string {
	classes, err := client.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err != nil || len(classes.Items) == 0 {
		return nil
	}
	for _, item := range classes.Items {
		if item.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
			value := item.Name
			return &value
		}
	}
	if len(classes.Items) == 1 {
		value := classes.Items[0].Name
		return &value
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func deployment(image string) *appsv1.Deployment {
	return namedDeployment(image, Namespace, Name)
}

func namedDeployment(image, namespace, name string) *appsv1.Deployment {
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
			Name: name, Namespace: namespace, Labels: labels(name),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": name,
			}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels(name)},
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
	image, namespace, name string,
) (Info, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		info, err := findPod(ctx, client, image, namespace, name)
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
	resource ...string,
) (Info, error) {
	namespace := Namespace
	name := Name
	if len(resource) == 1 && resource[0] != "" {
		name = resource[0]
	} else if len(resource) >= 2 {
		if resource[0] != "" {
			namespace = resource[0]
		}
		if resource[1] != "" {
			name = resource[1]
		}
	}
	list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + name,
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
