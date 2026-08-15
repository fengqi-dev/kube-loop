//go:build e2e

package dataplane

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	capacityPhysicalSessions = 4
	capacityStreamsPerWSS    = 4
	capacityPayloadBytes     = 32 << 10
	capacityRoundsPerUser    = 64
	defaultMaximumRSSBytes   = 256 << 20
	defaultMaximumRSSGrowth  = 128 << 20
	defaultMinimumMiBSecond  = 1.0
	capacityEchoScript       = `import socket, threading
PAYLOAD = 32768
def serve(connection):
    try:
        chunks = []
        remaining = PAYLOAD
        while remaining:
            chunk = connection.recv(remaining)
            if not chunk:
                return
            chunks.append(chunk)
            remaining -= len(chunk)
        connection.sendall(b"".join(chunks))
    finally:
        connection.close()

def hold(connection):
    try:
        connection.sendall(b"R")
        while connection.recv(65536):
            pass
    finally:
        connection.close()

def hold_server():
    server = socket.socket()
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("0.0.0.0", 19192))
    server.listen()
    while True:
        connection, _ = server.accept()
        threading.Thread(target=hold, args=(connection,), daemon=True).start()

threading.Thread(target=hold_server, daemon=True).start()
server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("0.0.0.0", 19191))
server.listen()
while True:
    connection, _ = server.accept()
    threading.Thread(target=serve, args=(connection,), daemon=True).start()
`
)

type capacityClient struct {
	forwarder *websocketmux.Forwarder
	control   net.Conn
	token     tunnel.SessionToken
	source    *e2eSessionSource
}

func (client *capacityClient) Close() error {
	if client == nil {
		return nil
	}
	var result error
	if client.control != nil {
		result = client.control.Close()
	}
	if client.forwarder != nil {
		result = errors.Join(result, client.forwarder.Close())
	}
	return result
}

func TestGatewayPodMultiUserCapacityRSSAndCleanup(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	kubeClient := kubeClient(t)
	namespace := "kubeloop-capacity-" + strings.ToLower(uuid.NewString()[:8])
	createNamespace(t, ctx, kubeClient, namespace)
	t.Cleanup(func() { deleteNamespace(t, kubeClient, namespace) })

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	installGatewayWithLimits(t, ctx, kubeClient, namespace, publicKey, &gatewayTestLimits{
		maxSessions: capacityPhysicalSessions, maxSessionsPerUser: 1, maxStreamsPerSession: capacityStreamsPerWSS,
	})
	service := waitForGateway(t, ctx, kubeClient, namespace)
	gatewayPod := readyPodByLabel(t, ctx, kubeClient, namespace, "app.kubernetes.io/name=kubeloop-gateway")
	capacityService := installCapacityEcho(t, ctx, kubeClient, namespace)
	publicAddress := net.JoinHostPort(reachableNodeAddress(t, ctx, kubeClient), fmt.Sprint(service.Spec.Ports[0].NodePort))
	waitForHTTP(t, ctx, "http://"+publicAddress+"/health/ready")
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{capacityService.Spec.ClusterIP}})
	if err != nil {
		t.Fatal(err)
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := relayticket.NewSigner(testKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	memoryCurve := map[string]uint64{}
	memoryCurve["ready"] = gatewayWorkingSetBytes(t, ctx, kubeClient, gatewayPod)
	clients := make([]*capacityClient, 0, capacityPhysicalSessions)
	defer func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}()
	for index := range capacityPhysicalSessions {
		client := startCapacityPodClient(
			t, ctx, signer, publicAddress, namespace, spec, specHash,
			fmt.Sprintf("capacity-identity-%d", index), uuid.NewString(),
		)
		clients = append(clients, client)
		waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_websocket_sessions", index+1)
		memoryCurve[fmt.Sprintf("users-%d", index+1)] = gatewayWorkingSetBytes(t, ctx, kubeClient, gatewayPod)
		if index == 0 {
			assertCapacityConnectionRejected(
				t, ctx, signer, publicAddress, namespace, specHash, client.source.identityID, uuid.NewString(),
			)
		}
	}

	// A second device for an existing Identity is rejected even before the
	// global limit can be exceeded. A new Identity is rejected at the global
	// physical WSS limit.
	assertCapacityConnectionRejected(t, ctx, signer, publicAddress, namespace, specHash, "capacity-overflow", uuid.NewString())
	assertCapacityPodHealth(t, ctx, publicAddress)

	payload := bytes.Repeat([]byte("k"), capacityPayloadBytes)
	waitCapacityTarget(t, ctx, clients[0], capacityService.Spec.ClusterIP, 19191, payload)
	started := time.Now()
	var group sync.WaitGroup
	errors := make(chan error, len(clients))
	for _, client := range clients {
		group.Add(1)
		go func(client *capacityClient) {
			defer group.Done()
			for range capacityRoundsPerUser {
				if err := capacityRoundTrip(ctx, client, capacityService.Spec.ClusterIP, 19191, payload); err != nil {
					errors <- err
					return
				}
			}
		}(client)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(started)
	transferred := float64(len(payload)*capacityRoundsPerUser*len(clients)) / (1 << 20)
	throughput := transferred / elapsed.Seconds()
	minimumThroughput := floatEnv(t, "KUBELOOP_E2E_GATEWAY_MIN_MIB_PER_SECOND", defaultMinimumMiBSecond)
	if throughput < minimumThroughput {
		t.Fatalf("single-Pod Gateway throughput %.2f MiB/s is below %.2f MiB/s", throughput, minimumThroughput)
	}
	t.Logf("single-Pod Gateway throughput: %.2f MiB/s (%0.2f MiB in %s)", throughput, transferred, elapsed)

	// Each WSS reserves one stream for the authoritative control Session. Hold
	// the remaining three streams per user open against a slow in-cluster target.
	var held []net.Conn
	for _, client := range clients {
		for range capacityStreamsPerWSS - 1 {
			connection := openCapacityStream(t, ctx, client, capacityService.Spec.ClusterIP, 19192)
			marker := make([]byte, 1)
			if _, err := io.ReadFull(connection, marker); err != nil || marker[0] != 'R' {
				t.Fatalf("hold capacity stream: marker=%q err=%v", marker, err)
			}
			held = append(held, connection)
		}
	}
	defer func() {
		for _, connection := range held {
			_ = connection.Close()
		}
	}()
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_connections", len(held)+len(clients))
	memoryCurve["logical-stream-limit"] = gatewayWorkingSetBytes(t, ctx, kubeClient, gatewayPod)
	assertCapacityPodHealth(t, ctx, publicAddress)
	assertStreamCapacityRejected(t, ctx, clients[0], capacityService.Spec.ClusterIP, 19192)
	for _, connection := range held {
		_ = connection.Close()
	}
	held = nil
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_connections", len(clients))

	// Closing the desktop transport models logout: capacity and all logical
	// resources must be released even while the Pod was at both limits.
	if err := clients[0].Close(); err != nil {
		t.Fatal(err)
	}
	clients = clients[1:]
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_websocket_sessions", capacityPhysicalSessions-1)
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_connections", capacityPhysicalSessions-1)
	replacement := startCapacityPodClient(
		t, ctx, signer, publicAddress, namespace, spec, specHash,
		"capacity-replacement", uuid.NewString(),
	)
	clients = append(clients, replacement)
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_websocket_sessions", capacityPhysicalSessions)
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_connections", capacityPhysicalSessions)
	if err := capacityRoundTrip(ctx, replacement, capacityService.Spec.ClusterIP, 19191, payload); err != nil {
		t.Fatalf("replacement user after full-capacity logout: %v", err)
	}

	for _, client := range clients {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}
	clients = nil
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_websocket_sessions", 0)
	waitGatewayMetric(t, ctx, publicAddress, "kubeloop_gateway_active_connections", 0)
	memoryCurve["clean"] = gatewayWorkingSetBytes(t, ctx, kubeClient, gatewayPod)
	assertMemoryCurve(t, memoryCurve)
}

func startCapacityPodClient(
	t *testing.T,
	ctx context.Context,
	signer *relayticket.Signer,
	gatewayAddress, namespace string,
	spec networkspec.Spec,
	specHash, identityID, deviceID string,
) *capacityClient {
	t.Helper()
	now := time.Now().UTC()
	session := clientremote.Session{
		ID: uuid.NewString(), Namespace: namespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(5 * time.Minute),
		NetworkSpec: spec, NetworkSpecHash: specHash,
	}
	source := &e2eSessionSource{signer: signer, session: session, identityID: identityID, deviceID: deviceID}
	ticketSource := source.RelayTicketSource("capacity")
	forwarder, err := websocketmux.Start(ctx, websocketmux.ClientConfig{
		URL: "ws://" + gatewayAddress + testPath, DeviceID: deviceID,
		TokenSource: func(ctx context.Context) (string, error) {
			ticket, err := ticketSource(ctx)
			return ticket.Ticket, err
		},
		PoolSize: 1, MaxPhysical: 1, MaxStreamsPerConn: capacityStreamsPerWSS,
	})
	if err != nil {
		t.Fatalf("connect capacity client: %v", err)
	}
	token, err := tunnel.RelaySessionToken(session.ID, session.Generation)
	if err != nil {
		_ = forwarder.Close()
		t.Fatal(err)
	}
	control, err := (&net.Dialer{}).DialContext(ctx, "tcp", forwarder.Address())
	if err == nil {
		err = tunnel.WriteAuthorizedControlSession(control, token, spec)
	}
	if err == nil {
		err = tunnel.ReadStatus(control)
	}
	if err != nil {
		if control != nil {
			_ = control.Close()
		}
		_ = forwarder.Close()
		t.Fatalf("register capacity Session: %v", err)
	}
	return &capacityClient{forwarder: forwarder, control: control, token: token, source: source}
}

func assertCapacityConnectionRejected(
	t *testing.T,
	ctx context.Context,
	signer *relayticket.Signer,
	gatewayAddress, namespace, specHash, identityID, deviceID string,
) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	ticket, err := signer.Sign(relayticket.Claims{
		Version: relayticket.Version, Issuer: testIssuer, Audience: testRelay,
		IdentityID: identityID, DeviceID: deviceID, SessionID: uuid.NewString(), SessionGeneration: 1,
		Namespace: namespace, Operations: []string{"tunnel"}, NetworkSpecHash: specHash,
		TicketID: uuid.NewString(), IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	forwarder, err := websocketmux.Start(ctx, websocketmux.ClientConfig{
		URL: "ws://" + gatewayAddress + testPath, Token: ticket, DeviceID: deviceID, PoolSize: 1, MaxPhysical: 1,
	})
	if forwarder != nil {
		_ = forwarder.Close()
	}
	if err == nil {
		t.Fatal("Gateway accepted a physical WSS beyond its capacity limit")
	}
}

func capacityRoundTrip(
	ctx context.Context,
	client *capacityClient,
	host string,
	port uint16,
	payload []byte,
) error {
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", client.forwarder.Address())
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if err := tunnel.WriteOpen(connection, tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: host, Port: port}, client.token); err != nil {
		return err
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		return err
	}
	if _, err := connection.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, got); err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return errors.New("Gateway capacity response mismatch")
	}
	return nil
}

func waitCapacityTarget(
	t *testing.T,
	ctx context.Context,
	client *capacityClient,
	host string,
	port uint16,
	payload []byte,
) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for {
		last = capacityRoundTrip(ctx, client, host, port, payload)
		if last == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("wait for capacity target %s: %v", net.JoinHostPort(host, strconv.Itoa(int(port))), last)
		case <-ticker.C:
		}
	}
}

func installCapacityEcho(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
) *corev1.Service {
	t.Helper()
	const name = "capacity-echo"
	labels := map[string]string{"app.kubernetes.io/name": name}
	_, err := client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name: name, Image: "python:3.12-alpine", ImagePullPolicy: corev1.PullIfNotPresent,
						Command: []string{"python", "-u", "-c", capacityEchoScript},
						Ports: []corev1.ContainerPort{
							{Name: "echo", ContainerPort: 19191},
							{Name: "hold", ContainerPort: 19192},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("echo")}},
							PeriodSeconds: 1, FailureThreshold: 30,
						},
					}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "echo", Port: 19191, TargetPort: intstr.FromString("echo")},
				{Name: "hold", Port: 19192, TargetPort: intstr.FromString("hold")},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	readyPodByLabel(t, ctx, client, namespace, "app.kubernetes.io/name="+name)
	return service
}

func openCapacityStream(
	t *testing.T,
	ctx context.Context,
	client *capacityClient,
	host string,
	port uint16,
) net.Conn {
	t.Helper()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", client.forwarder.Address())
	if err == nil {
		err = tunnel.WriteOpen(connection, tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: host, Port: port}, client.token)
	}
	if err == nil {
		err = tunnel.ReadStatus(connection)
	}
	if err != nil {
		if connection != nil {
			_ = connection.Close()
		}
		t.Fatal(err)
	}
	return connection
}

func assertStreamCapacityRejected(t *testing.T, ctx context.Context, client *capacityClient, host string, port uint16) {
	t.Helper()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", client.forwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if err := tunnel.WriteOpen(connection, tunnel.OpenRequest{Command: tunnel.CommandTCP, Host: host, Port: port}, client.token); err != nil {
		return
	}
	if err := tunnel.ReadStatus(connection); err == nil {
		t.Fatal("Gateway accepted a logical stream beyond the negotiated limit")
	}
}

func assertCapacityPodHealth(t *testing.T, ctx context.Context, gatewayAddress string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+gatewayAddress+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("Gateway capacity health %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("Gateway capacity health %s status=%d", path, response.StatusCode)
		}
	}
}

func waitGatewayMetric(t *testing.T, ctx context.Context, gatewayAddress, metric string, want int) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last int
	var lastErr error
	for {
		last, lastErr = gatewayMetric(ctx, gatewayAddress, metric)
		if lastErr == nil && last == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("Gateway metric %s=%d, want %d: %v", metric, last, want, lastErr)
		case <-ticker.C:
		}
	}
}

func gatewayMetric(ctx context.Context, gatewayAddress, metric string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+gatewayAddress+"/metrics", nil)
	if err != nil {
		return 0, err
	}
	response, err := (&http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("metrics status %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 64<<10))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == metric {
			value, err := strconv.Atoi(fields[1])
			return value, err
		}
	}
	return 0, fmt.Errorf("metric %s was not found: %w", metric, scanner.Err())
}

type kubeletStatsSummary struct {
	Pods []struct {
		PodRef struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"podRef"`
		Containers []struct {
			Name   string `json:"name"`
			Memory *struct {
				WorkingSetBytes *uint64 `json:"workingSetBytes"`
			} `json:"memory"`
		} `json:"containers"`
	} `json:"pods"`
}

func gatewayWorkingSetBytes(t *testing.T, ctx context.Context, client kubernetes.Interface, pod corev1.Pod) uint64 {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		raw, err := client.CoreV1().RESTClient().Get().AbsPath(
			"/api/v1/nodes/" + pod.Spec.NodeName + "/proxy/stats/summary",
		).DoRaw(ctx)
		if err == nil {
			var summary kubeletStatsSummary
			err = json.Unmarshal(raw, &summary)
			if err == nil {
				for _, item := range summary.Pods {
					if item.PodRef.Namespace != pod.Namespace || item.PodRef.Name != pod.Name {
						continue
					}
					for _, container := range item.Containers {
						if container.Name == "kubeloop-gateway" && container.Memory != nil &&
							container.Memory.WorkingSetBytes != nil {
							return *container.Memory.WorkingSetBytes
						}
					}
				}
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("read Gateway kubelet working set: %v", lastErr)
		case <-ticker.C:
		}
	}
}

func assertMemoryCurve(t *testing.T, curve map[string]uint64) {
	t.Helper()
	baseline := curve["ready"]
	peak := baseline
	for _, value := range curve {
		if value > peak {
			peak = value
		}
	}
	maximum := uint64(intEnv(t, "KUBELOOP_E2E_GATEWAY_MAX_RSS_BYTES", defaultMaximumRSSBytes))
	maximumGrowth := uint64(intEnv(t, "KUBELOOP_E2E_GATEWAY_MAX_RSS_GROWTH_BYTES", defaultMaximumRSSGrowth))
	if peak > maximum || peak-baseline > maximumGrowth {
		t.Fatalf("Gateway RSS curve exceeded bounds: curve=%v peak=%d baseline=%d max=%d maxGrowth=%d", curve, peak, baseline, maximum, maximumGrowth)
	}
	t.Logf("single-Pod Gateway working-set curve: %v (peak %.2f MiB, growth %.2f MiB)", curve, float64(peak)/(1<<20), float64(peak-baseline)/(1<<20))
}

func intEnv(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		t.Fatalf("%s must be a positive integer", name)
	}
	return value
}

func floatEnv(t *testing.T, name string, fallback float64) float64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		t.Fatalf("%s must be a positive number", name)
	}
	return value
}
