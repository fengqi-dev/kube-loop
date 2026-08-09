package gatewayruntime

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFindReturnsReadyGateway(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gateway-pod",
			Namespace: Namespace,
			Labels:    map[string]string{"app.kubernetes.io/name": Name},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.8",
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "gateway", Image: "example/gateway:latest",
		}}},
	})

	info, err := Find(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "gateway-pod" || info.IP != "10.0.0.8" {
		t.Fatalf("unexpected gateway info: %+v", info)
	}
}

func TestFindNamedSelectsPrivateGateway(t *testing.T) {
	readyPod := func(name, resourceName, ip string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: Namespace,
				Labels: map[string]string{"app.kubernetes.io/name": resourceName},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning, PodIP: ip,
				Conditions: []corev1.PodCondition{{
					Type: corev1.PodReady, Status: corev1.ConditionTrue,
				}},
			},
		}
	}
	client := fake.NewSimpleClientset(
		readyPod("shared-pod", Name, "10.0.0.7"),
		readyPod("private-pod", "kubeloop-gateway-abcd", "10.0.0.8"),
	)
	info, err := FindNamed(context.Background(), client, "kubeloop-gateway-abcd")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "private-pod" || info.IP != "10.0.0.8" {
		t.Fatalf("selected Gateway pod = %+v", info)
	}
}

func TestFindResourceSelectsPrivateNamespace(t *testing.T) {
	const (
		namespace = "kubeloop-system-abcd"
		name      = "kubeloop-gateway-abcd"
	)
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "private-pod", Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/name": name},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning, PodIP: "10.0.0.8",
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		},
	})
	info, err := FindResource(context.Background(), client, namespace, name)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "private-pod" || info.IP != "10.0.0.8" {
		t.Fatalf("selected Gateway pod = %+v", info)
	}
}

func TestFindPodSelectsRequestedImage(t *testing.T) {
	readyPod := func(name, image, ip string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: Namespace,
				Labels: map[string]string{"app.kubernetes.io/name": Name},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "gateway", Image: image,
			}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning, PodIP: ip,
				Conditions: []corev1.PodCondition{{
					Type: corev1.PodReady, Status: corev1.ConditionTrue,
				}},
			},
		}
	}
	client := fake.NewSimpleClientset(
		readyPod("old", "example/gateway:old", "10.0.0.7"),
		readyPod("new", "example/gateway:new", "10.0.0.8"),
	)
	info, err := findPod(context.Background(), client, "example/gateway:new")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "new" || info.IP != "10.0.0.8" {
		t.Fatalf("selected Gateway pod = %+v", info)
	}
}

func TestEnsureCreatesResourcesAndUsesLatestPullPolicy(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gateway-pod",
			Namespace: Namespace,
			Labels:    map[string]string{"app.kubernetes.io/name": Name},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.8",
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "gateway", Image: "example/gateway:latest",
		}}},
	})

	if _, err := Ensure(context.Background(), client, "example/gateway:latest"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Namespaces().Get(
		context.Background(), Namespace, metav1.GetOptions{},
	); err != nil {
		t.Fatal(err)
	}
	deployment, err := client.AppsV1().Deployments(Namespace).Get(
		context.Background(), Name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if container.Image != "example/gateway:latest" || container.ImagePullPolicy != corev1.PullAlways {
		t.Fatalf("unexpected gateway container: %+v", container)
	}
}

func TestEnsureResourceCreatesPrivateNamespaceAndDeployment(t *testing.T) {
	const (
		namespace = "kubeloop-system-abcd"
		name      = "kubeloop-gateway-abcd"
		image     = "example/gateway:v1"
	)
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "private-pod", Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/name": name},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning, PodIP: "10.0.0.8",
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "gateway", Image: image,
		}}},
	})
	if _, err := EnsureResource(context.Background(), client, image, namespace, name); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Namespaces().Get(
		context.Background(), namespace, metav1.GetOptions{},
	); err != nil {
		t.Fatal(err)
	}
	created, err := client.AppsV1().Deployments(namespace).Get(
		context.Background(), name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Namespace != namespace || created.Name != name {
		t.Fatalf("private deployment = %s/%s", created.Namespace, created.Name)
	}
}

func TestEnsureRejectsUnmanagedDeployment(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: Namespace}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name: Name, Namespace: Namespace,
		}},
	)

	_, err := Ensure(context.Background(), client, "example/gateway:v1")
	if err == nil || !strings.Contains(err.Error(), "not managed by kube-loop") {
		t.Fatalf("expected unmanaged deployment error, got %v", err)
	}
}

func TestInstallManifestUsesDefaultImage(t *testing.T) {
	manifest := InstallManifest("")
	for _, expected := range []string{Namespace, Name, defaultImage, "containerPort: 1080"} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("manifest missing %q", expected)
		}
	}
}

func TestInstallManifestNamedUsesPrivateResourceName(t *testing.T) {
	const name = "kubeloop-gateway-abcd"
	manifest := InstallManifestNamed("example/gateway:v1", name)
	if !strings.Contains(manifest, "name: "+name) ||
		!strings.Contains(manifest, "app.kubernetes.io/name: "+name) {
		t.Fatalf("private Gateway name missing from manifest:\n%s", manifest)
	}
}

func TestInstallManifestResourceUsesPrivateNamespace(t *testing.T) {
	const (
		namespace = "kubeloop-system-abcd"
		name      = "kubeloop-gateway-abcd"
	)
	manifest := InstallManifestResource("example/gateway:v1", namespace, name)
	for _, expected := range []string{"name: " + namespace, "namespace: " + namespace, "name: " + name} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("manifest missing %q:\n%s", expected, manifest)
		}
	}
}

func TestDeploymentIsUnprivileged(t *testing.T) {
	deployment := deployment("kube-loop-gateway:test")
	pod := deployment.Spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatal("service account token must not be mounted")
	}
	container := pod.Containers[0]
	if container.SecurityContext == nil ||
		container.SecurityContext.AllowPrivilegeEscalation == nil ||
		*container.SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("privilege escalation must be disabled")
	}
	if container.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("unexpected image pull policy %q", container.ImagePullPolicy)
	}
}

func TestDeploymentUsesLocalDevelopmentImageWithoutPulling(t *testing.T) {
	container := deployment("kube-loop-gateway:dev-deadbeef").
		Spec.Template.Spec.Containers[0]
	if container.ImagePullPolicy != corev1.PullNever {
		t.Fatalf("local development image pull policy = %q", container.ImagePullPolicy)
	}
}

func TestEnsureHTTPResourceCreatesSecretServiceAndEnabledDeployment(t *testing.T) {
	const image = "example/gateway:http"
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gateway-pod", Namespace: Namespace,
			Labels: map[string]string{"app.kubernetes.io/name": Name},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gateway", Image: image}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning, PodIP: "10.0.0.8",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	})
	if _, err := EnsureHTTPResource(
		context.Background(), client, image, Namespace, Name, "shared-token",
		"wss://gateway.example.com/v1/tunnel",
	); err != nil {
		t.Fatal(err)
	}
	secret, err := client.CoreV1().Secrets(Namespace).Get(
		context.Background(), Name+"-http", metav1.GetOptions{},
	)
	if err != nil || secret.StringData[TokenKey] != "shared-token" {
		t.Fatalf("HTTP Secret = %+v, err=%v", secret, err)
	}
	service, err := client.CoreV1().Services(Namespace).Get(
		context.Background(), Name+"-http", metav1.GetOptions{},
	)
	if err != nil || len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != HTTPPort {
		t.Fatalf("HTTP Service = %+v, err=%v", service, err)
	}
	deployment, err := client.AppsV1().Deployments(Namespace).Get(
		context.Background(), Name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if len(container.Env) != 1 || len(container.Ports) != 2 {
		t.Fatalf("HTTP Gateway container = %+v", container)
	}
	ingress, err := client.NetworkingV1().Ingresses(Namespace).Get(
		context.Background(), Name+"-http", metav1.GetOptions{},
	)
	if err != nil || ingress.Spec.Rules[0].Host != "gateway.example.com" {
		t.Fatalf("HTTP Ingress = %+v, err=%v", ingress, err)
	}
}

func TestEnsureHTTPResourceCreatesHostlessIngressForIPAddress(t *testing.T) {
	const image = "example/gateway:http"
	client := fake.NewSimpleClientset(
		&networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "nginx"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gateway-pod", Namespace: Namespace,
				Labels: map[string]string{"app.kubernetes.io/name": Name},
			},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gateway", Image: image}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning, PodIP: "10.0.0.8",
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	)
	if _, err := EnsureHTTPResource(
		context.Background(), client, image, Namespace, Name, "shared-token",
		"ws://192.168.66.5:30080/v1/tunnel",
	); err != nil {
		t.Fatal(err)
	}
	ingress, err := client.NetworkingV1().Ingresses(Namespace).Get(
		context.Background(), Name+"-http", metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if ingress.Spec.Rules[0].Host != "" {
		t.Fatalf("IP endpoint Ingress host = %q, want empty", ingress.Spec.Rules[0].Host)
	}
	if ingress.Spec.IngressClassName == nil || *ingress.Spec.IngressClassName != "nginx" {
		t.Fatalf("Ingress class = %v, want nginx", ingress.Spec.IngressClassName)
	}
}

func TestEnsureHTTPResourceSkipsIngressForLoopback(t *testing.T) {
	const image = "example/gateway:http"
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gateway-pod", Namespace: Namespace,
			Labels: map[string]string{"app.kubernetes.io/name": Name},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "gateway", Image: image}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning, PodIP: "10.0.0.8",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	})
	if _, err := EnsureHTTPResource(
		context.Background(), client, image, Namespace, Name, "shared-token",
		"ws://127.0.0.1:8080/v1/tunnel",
	); err != nil {
		t.Fatal(err)
	}
	ingresses, err := client.NetworkingV1().Ingresses(Namespace).List(
		context.Background(), metav1.ListOptions{},
	)
	if err != nil || len(ingresses.Items) != 0 {
		t.Fatalf("loopback Ingresses = %+v, err=%v", ingresses.Items, err)
	}
}

func TestHTTPIngressManifestIncludesEndpointAndMultiplexService(t *testing.T) {
	manifest := HTTPIngressManifestResource(
		"example/gateway:v1", Namespace, Name, "safe-token", "wss://gateway.example.com/v1/tunnel",
	)
	for _, expected := range []string{
		"kind: Secret", "c2FmZS10b2tlbg==", "kind: Service", "containerPort: 8080",
		"kind: Ingress", "host: gateway.example.com", "path: /v1/tunnel",
	} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("HTTP manifest missing %q:\n%s", expected, manifest)
		}
	}
	secretAt := strings.Index(manifest, "kind: Secret")
	deploymentAt := strings.Index(manifest, "kind: Deployment")
	serviceAt := strings.Index(manifest, "kind: Service")
	if !(secretAt < deploymentAt && deploymentAt < serviceAt) {
		t.Fatalf("HTTP resources must be ordered Secret, Deployment, Service:\n%s", manifest)
	}
}

func TestHTTPIngressManifestUsesHostlessRuleForIPAddress(t *testing.T) {
	manifest := HTTPIngressManifestResource(
		"example/gateway:v1", Namespace, Name, "safe-token",
		"ws://192.168.66.5:30080/v1/tunnel",
	)
	if !strings.Contains(manifest, "kind: Ingress") || strings.Contains(manifest, "host: 192.168.66.5") {
		t.Fatalf("IP endpoint manifest must use a hostless Ingress:\n%s", manifest)
	}
}
