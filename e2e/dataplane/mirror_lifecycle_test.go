//go:build e2e

package dataplane

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/mirrorapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

const mirrorLifecycleAccessToken = "e2e-mirror-lifecycle"

func TestRealMirrorPreservesPrimaryPathAndRecoversStaleOwner(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure real Mirror fixture: %v", err)
	}

	serviceName := "mirror-" + strings.ToLower(uuid.NewString()[:8])
	backendName := serviceName + "-backend"
	backendService, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: backendName, Namespace: harness.EchoNamespace},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": "kubeloop-e2e-echo"},
			Ports: []corev1.ServicePort{
				{Name: "tcp", Port: 8080, Protocol: corev1.ProtocolTCP},
				{Name: "udp", Port: 9090, Protocol: corev1.ProtocolUDP},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Mirror primary NodePort fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = kubeClient.CoreV1().Services(harness.EchoNamespace).Delete(
			cleanupContext, backendName, metav1.DeleteOptions{},
		)
	})
	service, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: serviceName, Namespace: harness.EchoNamespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "kubeloop-e2e-echo"},
			Ports: []corev1.ServicePort{
				{Name: "tcp", Port: 8080, Protocol: corev1.ProtocolTCP},
				{Name: "udp", Port: 9090, Protocol: corev1.ProtocolUDP},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create real Mirror Service: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = kubeClient.CoreV1().Services(harness.EchoNamespace).Delete(
			cleanupContext, serviceName, metav1.DeleteOptions{},
		)
		_ = kubeClient.DiscoveryV1().EndpointSlices(harness.EchoNamespace).Delete(
			cleanupContext, serviceName+"-kubeloop", metav1.DeleteOptions{},
		)
	})
	originalSelector := maps.Clone(service.Spec.Selector)
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "baseline", "cluster-tcp:")
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 9090, "udp", "baseline", "cluster-udp:")

	gatewayIP := reachableHostIP(t, ctx, kubeClient)
	primaryDial := mirrorNodePortDialer(reachableNodeAddress(t, ctx, kubeClient), backendService)
	stateStore, identity, activeSession, remoteSession := exchangeLifecycleState(t, ctx, service.Spec.ClusterIP)
	provider, err := controlplanekubernetes.NewForRESTConfig(kubeRESTConfig(t), controlplanekubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := controlplanekubernetes.NewServiceResolver(provider)
	if err != nil {
		t.Fatal(err)
	}
	bindingConfig, err := provider.SystemRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := trafficbindingclient.NewForRESTConfig(bindingConfig, trafficbindingclient.Config{
		ControlPlaneID: e2eTrafficControlPlaneID,
	})
	if err != nil {
		t.Fatal(err)
	}
	realMutator, err := mirrorapi.NewTrafficBindingResourceMutator(provider, stateStore, bindings)
	if err != nil {
		t.Fatal(err)
	}
	mutator := &failNextRestoreMutator{delegate: realMutator}
	handler, err := mirrorapi.New(
		stateStore,
		e2eExecSessionValidator{identityID: identity.Subject, session: activeSession},
		resolver,
		mutator,
		mirrorapi.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	server, gatewayClient := startMirrorLifecycleController(t, handler, identity, activeSession, gatewayIP, primaryDial)
	defer server.Close()

	serverProfile := profile.Profile{ID: "mirror-e2e", BaseURL: server.URL}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: mirrorLifecycleAccessToken,
			AccessExpiresAt: identity.AccessExpiresAt, RefreshToken: "unused",
			RefreshExpiresAt: identity.AccessExpiresAt, DeviceID: identity.DeviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{HTTPClient: gatewayClient})
	if err != nil {
		t.Fatal(err)
	}
	dataPlane := startE2EDataPlane(t, ctx, remoteClient, gatewayClient, serverProfile, remoteSession)
	manager, err := clientmirror.NewManager(remoteClient, clientmirror.Config{TrafficStreams: dataPlane})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = manager.Shutdown(shutdownContext)
	})

	tcpTarget, tcpAddress, tcpCopies := startMirrorTCPTarget(t)
	defer tcpTarget.Close()
	udpTarget, udpAddress, udpCopies := startMirrorUDPTarget(t)
	defer udpTarget.Close()
	targets := []clientmirror.LocalTarget{
		{ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: uint16(tcpAddress.Port)},
		{ServicePort: 9090, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: uint16(udpAddress.Port)},
	}

	// The original Pod response remains authoritative. Desktop responses use a
	// deliberately different prefix and must never appear on the cluster path.
	first := startRealMirror(t, ctx, manager, serverProfile, remoteSession, serviceName, targets)
	assertServiceIntercepted(t, ctx, kubeClient, stateStore, serviceName, gatewayIP, first.ID)
	waitForMirroredClusterProbe(
		t,
		ctx,
		kubeClient,
		tcpCopies,
		service.Spec.ClusterIP,
		8080,
		"tcp",
		"normal-tcp",
		"cluster-tcp:",
	)
	waitForMirroredClusterProbe(
		t,
		ctx,
		kubeClient,
		udpCopies,
		service.Spec.ClusterIP,
		9090,
		"udp",
		"normal-udp",
		"cluster-udp:",
	)
	stopContext, stopCancel := context.WithTimeout(ctx, 45*time.Second)
	if err := manager.Delete(stopContext, serverProfile.ID, first.ID); err != nil {
		stopCancel()
		t.Fatalf("delete real Mirror: %v", err)
	}
	stopCancel()
	waitForMirrorState(t, ctx, stateStore, first.ID, "stopped")
	assertServiceRestored(t, ctx, kubeClient, stateStore, serviceName, first.ID, originalSelector)
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "restored", "cluster-tcp:")

	// Abrupt desktop loss skips both the relay Stop frame and the DELETE API.
	// The stream owner must still restore the primary Service and discard its
	// durable rollback snapshot.
	crashed, err := remoteClient.CreateMirror(ctx, serverProfile, remoteSession, remote.MirrorSpec{
		Service: serviceName,
		Ports: []remote.MirrorPort{
			{ServicePort: 8080, Protocol: "tcp"},
			{ServicePort: 9090, Protocol: "udp"},
		},
	}, "mirror-client-crash:"+uuid.NewString())
	if err != nil {
		t.Fatalf("create Mirror for client crash: %v", err)
	}
	crashedConnection, err := dataPlane.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModeMirror, crashed.ID)
	if err != nil {
		t.Fatalf("open Mirror stream for client crash: %v", err)
	}
	waitForMirrorState(t, ctx, stateStore, crashed.ID, "running")
	assertServiceIntercepted(t, ctx, kubeClient, stateStore, serviceName, gatewayIP, crashed.ID)
	_ = crashedConnection.Close()
	waitForMirrorState(t, ctx, stateStore, crashed.ID, "failed")
	assertServiceRestored(t, ctx, kubeClient, stateStore, serviceName, crashed.ID, originalSelector)
	harness.WaitClusterProbe(
		t,
		ctx,
		kubeClient,
		service.Spec.ClusterIP,
		8080,
		"tcp",
		"after-client-crash",
		"cluster-tcp:",
	)

	// A replacement Control Plane must compensate a stale Mirror from the durable
	// Service snapshot when the original owner fails during restoration.
	second := startRealMirror(t, ctx, manager, serverProfile, remoteSession, serviceName, targets)
	assertServiceIntercepted(t, ctx, kubeClient, stateStore, serviceName, gatewayIP, second.ID)
	waitForMirroredClusterProbe(
		t,
		ctx,
		kubeClient,
		tcpCopies,
		service.Spec.ClusterIP,
		8080,
		"tcp",
		"before-crash",
		"cluster-tcp:",
	)
	mutator.failOneRestore()
	stopContext, stopCancel = context.WithTimeout(ctx, 45*time.Second)
	if err := manager.Pause(stopContext, serverProfile.ID, second.ID); err != nil {
		stopCancel()
		t.Fatalf("pause Mirror during simulated Control Plane loss: %v", err)
	}
	stopCancel()
	waitForMirrorState(t, ctx, stateStore, second.ID, "recovering")
	assertSnapshotCount(t, stateStore, second.ID, 0)
	time.Sleep(150 * time.Millisecond)
	reconciler, err := trafficbindingclient.NewReconciler(
		bindings, stateStore.Tasks(), stateStore.Sessions(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		trafficbindingclient.ReconcilerConfig{
			Interval: 100 * time.Millisecond, StaleAfter: 100 * time.Millisecond, CleanupTimeout: 5 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered, recoverErr := reconciler.RunOnce(ctx); recoverErr != nil || recovered < 1 {
		t.Fatalf("recover stale real Mirror: recovered=%d err=%v", recovered, recoverErr)
	}
	waitForMirrorState(t, ctx, stateStore, second.ID, "failed")
	assertServiceRestored(t, ctx, kubeClient, stateStore, serviceName, second.ID, originalSelector)
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "after-recovery", "cluster-tcp:")
	if err := manager.Delete(ctx, serverProfile.ID, second.ID); err != nil {
		t.Fatalf("delete recovered Mirror: %v", err)
	}

}

func mirrorNodePortDialer(
	nodeAddress string,
	service *corev1.Service,
) func(context.Context, string, string) (net.Conn, error) {
	targets := make(map[string]string, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		protocol := strings.ToLower(string(port.Protocol))
		targets[protocol+"/"+strconv.Itoa(int(port.Port))] = net.JoinHostPort(
			nodeAddress, strconv.Itoa(int(port.NodePort)),
		)
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		_, rawPort, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse Mirror primary address: %w", err)
		}
		target := targets[strings.ToLower(network)+"/"+rawPort]
		if target == "" {
			return nil, fmt.Errorf("no Mirror NodePort fixture for %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
}

func startMirrorLifecycleController(
	t *testing.T,
	handler *mirrorapi.Service,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	gatewayIP string,
	primaryDial func(context.Context, string, string) (net.Conn, error),
) (*httptest.Server, *http.Client) {
	t.Helper()
	gateway := startE2ETrafficGateway(t, gatewayIP, handler, primaryDial)
	policy := authorization.NewAuthenticated()
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://127.0.0.1"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					if request.Header.Get("Authorization") != "Bearer "+mirrorLifecycleAccessToken {
						return controlplaneapi.Identity{}, &controlplaneapi.Error{
							Code:    controlplaneapi.CodeUnauthenticated,
							Message: "invalid e2e access token",
						}
					}
					return identity, nil
				},
			),
		),
		controlplane.WithAuthorizer(policy),
		controlplane.WithAPIRoutes(controlplane.APIRoutes{
			Tickets: ticketapi.NewRoutes(gateway.tickets, e2eExecSessionValidator{
				identityID: identity.Subject, session: session,
			}).Endpoints(),
			Mirrors: mirrorapi.NewRoutes(handler).Endpoints(),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return startE2EControlPlaneServer(t, server.Handler(), gateway)
}

func startRealMirror(
	t *testing.T,
	ctx context.Context,
	manager *clientmirror.Manager,
	serverProfile profile.Profile,
	session remote.Session,
	service string,
	targets []clientmirror.LocalTarget,
) clientmirror.Info {
	t.Helper()
	startContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := manager.Start(startContext, serverProfile, session, clientmirror.Request{
		ProfileID: serverProfile.ID, Service: service, Targets: targets,
	})
	if err != nil {
		t.Fatalf("start real Mirror: %v", err)
	}
	return info
}

func waitForMirrorState(
	t *testing.T,
	ctx context.Context,
	stateStore *storage.Store,
	taskID, want string,
) storage.Task {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := stateStore.Tasks().GetByID(ctx, taskID)
		if err == nil && string(task.State) == want {
			return task
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("Mirror Task %s did not reach %s: task=%#v err=%v", taskID, want, task, err)
		case <-ticker.C:
		}
	}
}

func startMirrorTCPTarget(t *testing.T) (net.Listener, *net.TCPAddr, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	copies := make(chan string, 32)
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buffer := make([]byte, 256)
				count, readErr := conn.Read(buffer)
				if readErr == nil && count > 0 {
					copies <- string(buffer[:count])
					_, _ = fmt.Fprintf(conn, "desktop-shadow-tcp:%s", buffer[:count])
				}
			}(connection)
		}
	}()
	return listener, listener.Addr().(*net.TCPAddr), copies
}

func startMirrorUDPTarget(t *testing.T) (net.PacketConn, *net.UDPAddr, <-chan string) {
	t.Helper()
	connection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	copies := make(chan string, 32)
	go func() {
		buffer := make([]byte, 65507)
		for {
			count, address, readErr := connection.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			payload := string(buffer[:count])
			copies <- payload
			_, _ = connection.WriteTo([]byte("desktop-shadow-udp:"+payload), address)
		}
	}()
	return connection, connection.LocalAddr().(*net.UDPAddr), copies
}

func waitForMirroredClusterProbe(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	copies <-chan string,
	host string,
	port int,
	protocol, payload, primaryPrefix string,
) {
	t.Helper()
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	var lastResponse string
	var lastErr error
	var lastCopy string
	for {
		response, err := harness.ProbeFromCluster(ctx, client, host, port, protocol, payload)
		lastResponse, lastErr = response, err
		if strings.HasPrefix(response, "desktop-shadow-") {
			t.Fatalf("local Mirror response leaked onto primary path: %q", response)
		}
		if err == nil && strings.HasPrefix(response, primaryPrefix) {
			copyDeadline := time.NewTimer(2 * time.Second)
		copyWait:
			for {
				select {
				case got := <-copies:
					lastCopy = got
					if got == payload {
						if !copyDeadline.Stop() {
							select {
							case <-copyDeadline.C:
							default:
							}
						}
						return
					}
				case <-copyDeadline.C:
					break copyWait
				case <-ctx.Done():
					copyDeadline.Stop()
					t.Fatal(ctx.Err())
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf(
				"Mirror did not converge for %s %s:%d payload %q: response=%q error=%v last-copy=%q",
				protocol, host, port, payload, lastResponse, lastErr, lastCopy,
			)
		case <-time.After(time.Second):
		}
	}
}
