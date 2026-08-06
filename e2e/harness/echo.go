//go:build e2e

package harness

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

//go:embed manifests/echo.yaml
var echoManifestYAML []byte

func EnsureEchoNamespace(ctx context.Context, client kubernetes.Interface) error {
	ns, _, _, err := decodeEchoManifest()
	if err != nil {
		return err
	}
	return waitAndCreateEchoNamespace(ctx, client, ns)
}

func waitAndCreateEchoNamespace(ctx context.Context, client kubernetes.Interface, ns *corev1.Namespace) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		existing, err := client.CoreV1().Namespaces().Get(ctx, EchoNamespace, metav1.GetOptions{})
		switch {
		case err == nil && existing.DeletionTimestamp == nil:
			return nil
		case err == nil:
			// Namespace is terminating; wait until it is gone before recreating.
		case apierrors.IsNotFound(err):
			_, createErr := client.CoreV1().Namespaces().Create(ctx, ns.DeepCopy(), metav1.CreateOptions{})
			if createErr == nil || apierrors.IsAlreadyExists(createErr) {
				return nil
			}
			if !apierrors.IsForbidden(createErr) || !strings.Contains(createErr.Error(), "being terminated") {
				return createErr
			}
		default:
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("namespace %s still terminating", EchoNamespace)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func EnsureEchoWorkload(ctx context.Context, client kubernetes.Interface) error {
	ns, deploy, service, err := decodeEchoManifest()
	if err != nil {
		return err
	}
	if err := waitAndCreateEchoNamespace(ctx, client, ns); err != nil {
		return err
	}
	if existing, err := client.AppsV1().Deployments(EchoNamespace).Get(ctx, deploy.Name, metav1.GetOptions{}); err == nil {
		deploy.ResourceVersion = existing.ResourceVersion
		_, err = client.AppsV1().Deployments(EchoNamespace).Update(ctx, deploy, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		_, err = client.AppsV1().Deployments(EchoNamespace).Create(ctx, deploy, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		return err
	}

	if existing, err := client.CoreV1().Services(EchoNamespace).Get(ctx, service.Name, metav1.GetOptions{}); err == nil {
		service.ResourceVersion = existing.ResourceVersion
		service.Spec.ClusterIP = existing.Spec.ClusterIP
		_, err = client.CoreV1().Services(EchoNamespace).Update(ctx, service, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	} else if apierrors.IsNotFound(err) {
		_, err = client.CoreV1().Services(EchoNamespace).Create(ctx, service, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		return err
	}

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		pods, err := client.CoreV1().Pods(EchoNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=kubeloop-e2e-echo",
		})
		if err == nil {
			for _, pod := range pods.Items {
				if pod.Status.Phase == corev1.PodRunning {
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
							return nil
						}
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("echo workload not ready")
}

func decodeEchoManifest() (*corev1.Namespace, *appsv1.Deployment, *corev1.Service, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(echoManifestYAML), 4096)
	var (
		ns      *corev1.Namespace
		deploy  *appsv1.Deployment
		service *corev1.Service
	)
	for {
		var raw runtime.RawExtension
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, nil, fmt.Errorf("decode echo manifest: %w", err)
		}
		if len(bytes.TrimSpace(raw.Raw)) == 0 {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(raw.Raw, nil, nil)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("decode echo manifest object: %w", err)
		}
		switch typed := obj.(type) {
		case *corev1.Namespace:
			ns = typed
		case *appsv1.Deployment:
			deploy = typed
		case *corev1.Service:
			service = typed
		default:
			return nil, nil, nil, fmt.Errorf("unexpected echo manifest type %T", obj)
		}
	}
	if ns == nil || deploy == nil || service == nil {
		return nil, nil, nil, fmt.Errorf("echo manifest missing namespace, deployment, or service")
	}
	return ns, deploy, service, nil
}

func StartLocalTCPEcho(t *testing.T, prefix string) (net.Listener, *net.TCPAddr) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				_, _ = fmt.Fprintf(c, "%s:%s", prefix, string(buf[:n]))
			}(conn)
		}
	}()
	return listener, listener.Addr().(*net.TCPAddr)
}

func StartLocalUDPEcho(t *testing.T, prefix string) (net.PacketConn, *net.UDPAddr) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo([]byte(fmt.Sprintf("%s:%s", prefix, string(buf[:n]))), addr)
		}
	}()
	return conn, conn.LocalAddr().(*net.UDPAddr)
}

func WaitClusterProbe(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	host string,
	port int,
	protocol, payload, prefix string,
) string {
	t.Helper()
	got, err := waitClusterProbe(ctx, client, host, port, protocol, payload, prefix, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// WaitClusterProbeOptional is WaitClusterProbe but returns the error instead of
// failing the test. Useful when a host-side assertion already covers the path.
func WaitClusterProbeOptional(
	ctx context.Context,
	client kubernetes.Interface,
	host string,
	port int,
	protocol, payload, prefix string,
	timeout time.Duration,
) (string, error) {
	return waitClusterProbe(ctx, client, host, port, protocol, payload, prefix, timeout)
}

func waitClusterProbe(
	ctx context.Context,
	client kubernetes.Interface,
	host string,
	port int,
	protocol, payload, prefix string,
	timeout time.Duration,
) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last string
	var lastErr error
	for {
		if err := probeCtx.Err(); err != nil {
			return last, fmt.Errorf(
				"probe %s %s:%d canceled: %w (last error=%v, last=%q)",
				protocol, host, port, err, lastErr, last,
			)
		}
		got, err := ProbeFromCluster(probeCtx, client, host, port, protocol, payload)
		if err == nil && strings.HasPrefix(got, prefix) {
			return got, nil
		}
		last, lastErr = got, err
		select {
		case <-probeCtx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func ProbeFromCluster(
	ctx context.Context,
	client kubernetes.Interface,
	host string,
	port int,
	protocol, payload string,
) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := probeCtx.Err(); err != nil {
		return "", err
	}

	name := fmt.Sprintf("probe-%s-%d", protocol, time.Now().UnixNano())
	script := fmt.Sprintf(`
import socket, sys
host, port, payload = %q, %d, %q
if %q == "udp":
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.settimeout(5)
    s.sendto(payload.encode(), (host, port))
    data, _ = s.recvfrom(128)
else:
    s = socket.create_connection((host, port), timeout=5)
    s.sendall(payload.encode())
    data = s.recv(128)
sys.stdout.write(data.decode(errors="replace"))
`, host, port, payload, protocol)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: EchoNamespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   "python:3.12-alpine",
				Command: []string{"python", "-u", "-c", script},
			}},
		},
	}
	if _, err := client.CoreV1().Pods(EchoNamespace).Create(probeCtx, pod, metav1.CreateOptions{}); err != nil {
		return "", err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		_ = client.CoreV1().Pods(EchoNamespace).Delete(cleanupCtx, name, metav1.DeleteOptions{})
	}()

	for {
		current, err := client.CoreV1().Pods(EchoNamespace).Get(probeCtx, name, metav1.GetOptions{})
		if err != nil {
			select {
			case <-probeCtx.Done():
				return "", probeCtx.Err()
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		switch current.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			logs, err := client.CoreV1().Pods(EchoNamespace).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(probeCtx)
			if err != nil {
				return "", err
			}
			if current.Status.Phase == corev1.PodFailed {
				return string(logs), fmt.Errorf("probe failed: %s", string(logs))
			}
			return string(logs), nil
		}
		select {
		case <-probeCtx.Done():
			return "", probeCtx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}
