//go:build e2e

package dataplane

import (
	"context"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/exchangeapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

const exchangeLifecycleAccessToken = "e2e-exchange-lifecycle"

func TestRealExchangeLifecycleAndStaleOwnerRecovery(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure real Exchange fixture: %v", err)
	}

	serviceName := "exchange-" + strings.ToLower(uuid.NewString()[:8])
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
		t.Fatalf("create real Exchange Service: %v", err)
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
	stateStore, identity, activeSession, remoteSession := exchangeLifecycleState(
		t, ctx, service.Spec.ClusterIP,
	)
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
	realMutator, err := exchangeapi.NewTrafficBindingResourceMutator(provider, bindings)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := exchangeapi.New(
		e2eExecSessionValidator{identityID: identity.Subject, session: activeSession},
		resolver,
		realMutator,
		exchangeapi.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	server, gatewayClient := startExchangeLifecycleController(t, handler, identity, activeSession, gatewayIP)
	defer server.Close()

	serverProfile := profile.Profile{ID: "exchange-e2e", BaseURL: server.URL}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: exchangeLifecycleAccessToken,
			AccessExpiresAt: identity.AccessExpiresAt, RefreshToken: "unused",
			RefreshExpiresAt: identity.AccessExpiresAt, DeviceID: identity.DeviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{HTTPClient: gatewayClient})
	if err != nil {
		t.Fatal(err)
	}
	dataPlane := startE2EDataPlane(t, ctx, remoteClient, gatewayClient, serverProfile, remoteSession)
	manager, err := clientexchange.NewManager(remoteClient, clientexchange.Config{TrafficStreams: dataPlane})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = manager.Shutdown(shutdownContext)
	})

	tcpTarget, tcpAddress := harness.StartLocalTCPEcho(t, "desktop-tcp")
	defer tcpTarget.Close()
	udpTarget, udpAddress := harness.StartLocalUDPEcho(t, "desktop-udp")
	defer udpTarget.Close()
	targets := []clientexchange.LocalTarget{
		{ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: uint16(tcpAddress.Port)},
		{ServicePort: 9090, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: uint16(udpAddress.Port)},
	}

	// The user-facing lifecycle is create, stop, then delete. Stop restores
	// Kubernetes resources but retains the paused TrafficBinding for recovery;
	// delete removes that durable intent.
	first := startRealExchange(t, ctx, manager, serverProfile, remoteSession, serviceName, targets)
	assertServiceIntercepted(t, ctx, kubeClient, stateStore, serviceName, gatewayIP, first.ID)
	assertTrafficBindingActive(ctx, t, bindingConfig, harness.EchoNamespace, first.ID, "Exchange")
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "normal", "desktop-tcp:")
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 9090, "udp", "normal", "desktop-udp:")
	stopContext, stopCancel := context.WithTimeout(ctx, 45*time.Second)
	if err := manager.Pause(stopContext, serverProfile.ID, first.ID); err != nil {
		stopCancel()
		t.Fatalf("stop real Exchange: %v", err)
	}
	stopCancel()
	waitForRealExchangeState(t, ctx, stateStore, first.ID, "stopped")
	assertTrafficBindingPaused(ctx, t, bindingConfig, harness.EchoNamespace, first.ID)
	assertServiceRestored(t, ctx, kubeClient, stateStore, serviceName, first.ID, originalSelector)
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "restored", "cluster-tcp:")
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown Exchange manager before restore: %v", err)
	}
	restoredManager, err := clientexchange.NewManager(
		remoteClient,
		clientexchange.Config{TrafficStreams: dataPlane},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = restoredManager.Shutdown(shutdownContext)
	})
	if err := restoredManager.Restore(ctx, serverProfile, remoteSession); err != nil {
		t.Fatalf("restore stopped Exchange after client restart: %v", err)
	}
	restored := restoredManager.List(serverProfile.ID)
	if len(restored) != 1 || restored[0].ID != first.ID || restored[0].State != "paused" {
		t.Fatalf("restored Exchange list = %#v", restored)
	}
	if _, err := restoredManager.Resume(ctx, serverProfile.ID, first.ID); err != nil {
		t.Fatalf("resume restored Exchange: %v", err)
	}
	waitForRealExchangeState(t, ctx, stateStore, first.ID, "running")
	assertTrafficBindingActive(ctx, t, bindingConfig, harness.EchoNamespace, first.ID, "Exchange")
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "resumed", "desktop-tcp:")
	if err := restoredManager.Pause(ctx, serverProfile.ID, first.ID); err != nil {
		t.Fatalf("stop restored Exchange: %v", err)
	}
	waitForRealExchangeState(t, ctx, stateStore, first.ID, "stopped")
	assertTrafficBindingPaused(ctx, t, bindingConfig, harness.EchoNamespace, first.ID)
	if err := restoredManager.Delete(ctx, serverProfile.ID, first.ID); err != nil {
		t.Fatalf("delete stopped Exchange: %v", err)
	}
	waitForRealExchangeState(t, ctx, stateStore, first.ID, "deleted")
	assertTrafficBindingAbsent(ctx, t, bindingConfig, harness.EchoNamespace, first.ID)
	manager = restoredManager

	// A desktop process can disappear without sending either the relay Stop
	// frame or the DELETE request. Closing the underlying WebSocket abruptly
	// must still release the owner lease and restore the real Service.
	crashed, err := remoteClient.CreateExchange(ctx, serverProfile, remoteSession, remote.ExchangeSpec{
		Service: serviceName,
		Ports: []remote.ExchangePort{
			{ServicePort: 8080, Protocol: "tcp"},
			{ServicePort: 9090, Protocol: "udp"},
		},
	}, "exchange-client-crash:"+uuid.NewString())
	if err != nil {
		t.Fatalf("create Exchange for client crash: %v", err)
	}
	crashedConnection, err := dataPlane.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModeExchange, crashed.ID)
	if err != nil {
		t.Fatalf("open Exchange stream for client crash: %v", err)
	}
	waitForRealExchangeState(t, ctx, stateStore, crashed.ID, "running")
	assertServiceIntercepted(t, ctx, kubeClient, stateStore, serviceName, gatewayIP, crashed.ID)
	_ = crashedConnection.Close()
	waitForRealExchangeState(t, ctx, stateStore, crashed.ID, "failed")
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

	// Simulate startup recovery reading the durable CRD directly. Pausing that
	// CRD must restore the Service without any database Task record.
	second := startRealExchange(t, ctx, manager, serverProfile, remoteSession, serviceName, targets)
	assertServiceIntercepted(t, ctx, kubeClient, stateStore, serviceName, gatewayIP, second.ID)
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "before-crash", "desktop-tcp:")
	assertSnapshotCount(t, stateStore, second.ID, 0)
	if err := bindings.Pause(ctx, harness.EchoNamespace, second.ID); err != nil {
		t.Fatalf("recover real Exchange from its TrafficBinding: %v", err)
	}
	waitForRealExchangeState(t, ctx, stateStore, second.ID, "stopped")
	assertServiceRestored(t, ctx, kubeClient, stateStore, serviceName, second.ID, originalSelector)
	harness.WaitClusterProbe(t, ctx, kubeClient, service.Spec.ClusterIP, 8080, "tcp", "after-recovery", "cluster-tcp:")
	if err := bindings.Delete(ctx, harness.EchoNamespace, second.ID); err != nil {
		t.Fatalf("delete recovered Exchange: %v", err)
	}
	assertTrafficBindingAbsent(ctx, t, bindingConfig, harness.EchoNamespace, second.ID)
}

func exchangeLifecycleState(
	t *testing.T,
	ctx context.Context,
	serviceIP string,
) (*storage.Store, controlplaneapi.Identity, sessionapi.ActiveSession, remote.Session) {
	t.Helper()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "exchange-lifecycle.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	identityID, authorizationID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	deviceID := "exchange-e2e-device"
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(10 * time.Minute)
	createOAuthGrant(t, ctx, stateStore, authorizationID, identityID, deviceID, 8, now, expiresAt)
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{serviceIP}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	identity := controlplaneapi.Identity{
		Subject: identityID, DeviceID: deviceID, AuthorizationID: authorizationID, AccessExpiresAt: expiresAt,
	}
	active := sessionapi.ActiveSession{
		ID: sessionID, Namespace: harness.EchoNamespace, Generation: 1,
		ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
	}
	clientSession := remote.Session{
		ID: sessionID, Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
		NetworkSpec: network, NetworkSpecHash: networkHash,
	}
	return stateStore, identity, active, clientSession
}

func startExchangeLifecycleController(
	t *testing.T,
	handler *exchangeapi.Service,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	gatewayIP string,
) (*httptest.Server, *http.Client) {
	t.Helper()
	gateway := startE2ETrafficGateway(t, gatewayIP, handler, nil)
	policy := authorization.NewAuthenticated()
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://127.0.0.1"},
		controlplane.BuildInfo{},
		slog.Default(),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					if request.Header.Get("Authorization") != "Bearer "+exchangeLifecycleAccessToken {
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
			Exchanges: exchangeapi.NewRoutes(handler).Endpoints(),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return startE2EControlPlaneServer(t, server.Handler(), gateway)
}

func startRealExchange(
	t *testing.T,
	ctx context.Context,
	manager *clientexchange.Manager,
	serverProfile profile.Profile,
	session remote.Session,
	service string,
	targets []clientexchange.LocalTarget,
) clientexchange.Info {
	t.Helper()
	startContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := manager.Start(startContext, serverProfile, session, clientexchange.Request{
		ProfileID: serverProfile.ID, Service: service, Targets: targets,
	})
	if err != nil {
		t.Fatalf("start real Exchange: %v", err)
	}
	return info
}

func assertServiceIntercepted(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	stateStore *storage.Store,
	serviceName, gatewayIP, taskID string,
) {
	t.Helper()
	var lastService *corev1.Service
	var lastSlices []discoveryv1.EndpointSlice
	err := wait.PollUntilContextTimeout(
		ctx,
		100*time.Millisecond,
		20*time.Second,
		true,
		func(pollCtx context.Context) (bool, error) {
			service, err := client.CoreV1().
				Services(harness.EchoNamespace).
				Get(pollCtx, serviceName, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			lastService = service
			if len(service.Spec.Selector) != 0 {
				return false, nil
			}
			slices, err := client.DiscoveryV1().EndpointSlices(harness.EchoNamespace).List(pollCtx, metav1.ListOptions{
				LabelSelector: servicebinding.ServiceNameLabel + "=" + serviceName,
			})
			if err != nil {
				return false, nil
			}
			lastSlices = slices.Items
			foundGateway := false
			for _, slice := range slices.Items {
				for _, endpoint := range slice.Endpoints {
					if len(endpoint.Addresses) != 1 || endpoint.Addresses[0] != gatewayIP {
						return false, nil
					}
					foundGateway = true
				}
			}
			return foundGateway, nil
		},
	)
	if err != nil {
		t.Fatalf(
			"Service intercept did not converge exclusively to Gateway %s: service=%#v slices=%#v err=%v",
			gatewayIP, lastService, lastSlices, err,
		)
	}
	assertSnapshotCount(t, stateStore, taskID, 0)
}

func assertServiceRestored(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	stateStore *storage.Store,
	serviceName, taskID string,
	wantSelector map[string]string,
) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		service, err := client.CoreV1().Services(harness.EchoNamespace).Get(ctx, serviceName, metav1.GetOptions{})
		if err == nil && maps.Equal(service.Spec.Selector, wantSelector) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("Service %s selector was not restored", serviceName)
		case <-ticker.C:
		}
	}
	assertSnapshotCount(t, stateStore, taskID, 0)
}

func assertSnapshotCount(t *testing.T, stateStore *storage.Store, taskID string, want int) {
	t.Helper()
	snapshots, err := stateStore.ResourceSnapshots().ListByTask(context.Background(), taskID)
	if err != nil || len(snapshots) != want {
		t.Fatalf("Task rollback snapshots for %s=%d want=%d err=%v", taskID, len(snapshots), want, err)
	}
}

func waitForRealExchangeState(
	t *testing.T,
	ctx context.Context,
	_ *storage.Store,
	taskID, want string,
) {
	t.Helper()
	bindings, err := trafficbindingclient.NewForRESTConfig(
		kubeRESTConfig(t), trafficbindingclient.Config{ControlPlaneID: e2eTrafficControlPlaneID},
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		binding, getErr := bindings.GetSession(ctx, harness.EchoNamespace, taskID)
		state := "deleted"
		if getErr == nil {
			state = string(trafficsession.State(binding))
		}
		if state == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("Exchange Session %s did not reach %s: state=%s err=%v", taskID, want, state, getErr)
		case <-ticker.C:
		}
	}
}

func reachableHostIP(t *testing.T, ctx context.Context, client kubernetes.Interface) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("KUBELOOP_E2E_HOST_IP")); configured != "" {
		if address := net.ParseIP(configured); address == nil || address.IsUnspecified() {
			t.Fatalf("KUBELOOP_E2E_HOST_IP %q is not a concrete IP", configured)
		}
		if err := probeHostIP(ctx, client, configured); err != nil {
			t.Fatalf("KUBELOOP_E2E_HOST_IP %s is not reachable from Minikube: %v", configured, err)
		}
		return configured
	}
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var nodeIPs []net.IP
	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP {
				if parsed := net.ParseIP(address.Address).To4(); parsed != nil {
					nodeIPs = append(nodeIPs, parsed)
				}
			}
		}
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var attempted []string
	for _, networkInterface := range interfaces {
		addresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			continue
		}
		for _, raw := range addresses {
			ip, _, parseErr := net.ParseCIDR(raw.String())
			if parseErr != nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			candidate := ip.To4()
			if !sharesIPv4Prefix(candidate, nodeIPs) {
				continue
			}
			attempted = append(attempted, candidate.String())
			if probeErr := probeHostIP(ctx, client, candidate.String()); probeErr == nil {
				return candidate.String()
			}
		}
	}
	t.Fatalf("no host IP reachable from Minikube; tried %v (set KUBELOOP_E2E_HOST_IP to override)", attempted)
	return ""
}

func sharesIPv4Prefix(candidate net.IP, nodes []net.IP) bool {
	for _, node := range nodes {
		if len(node) == net.IPv4len && candidate[0] == node[0] && candidate[1] == node[1] && candidate[2] == node[2] {
			return true
		}
	}
	return false
}

func probeHostIP(ctx context.Context, client kubernetes.Interface, host string) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return err
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
		buffer := make([]byte, 64)
		count, readErr := connection.Read(buffer)
		if readErr == nil {
			_, _ = connection.Write([]byte("host-probe:" + string(buffer[:count])))
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	_, probeErr := harness.WaitClusterProbeOptional(
		ctx, client, host, port, "tcp", "hello", "host-probe:", 30*time.Second,
	)
	_ = listener.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	return probeErr
}
