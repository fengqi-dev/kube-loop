package gatewayruntime

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	})

	info, err := Find(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "gateway-pod" || info.IP != "10.0.0.8" {
		t.Fatalf("unexpected gateway info: %+v", info)
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
