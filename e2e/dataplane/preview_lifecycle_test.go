//go:build e2e

package dataplane

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/previewapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficsession"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

const previewLifecycleAccessToken = "e2e-preview-lifecycle"

func TestRealPreviewLifecycleOwnershipAndStaleRecovery(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure real Preview fixture: %v", err)
	}

	stateStore, identity, activeSession, remoteSession := previewLifecycleState(
		t, ctx, harness.EchoServiceIP(t, ctx, kubeClient),
	)
	providerConfig := kubeRESTConfig(t)
	provider, err := controlplanekubernetes.NewForRESTConfig(providerConfig, controlplanekubernetes.Config{})
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
	realResources, err := previewapi.NewTrafficBindingResourceManager(bindings)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := previewapi.New(
		e2eExecSessionValidator{identityID: identity.Subject, session: activeSession},
		realResources,
		previewapi.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	gatewayIP := reachableHostIP(t, ctx, kubeClient)
	server, gatewayClient := startPreviewLifecycleController(t, handler, identity, activeSession, gatewayIP)
	defer server.Close()

	serverProfile := profile.Profile{ID: "preview-e2e", BaseURL: server.URL}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: previewLifecycleAccessToken,
			AccessExpiresAt: identity.AccessExpiresAt, RefreshToken: "unused",
			RefreshExpiresAt: identity.AccessExpiresAt, DeviceID: identity.DeviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{HTTPClient: gatewayClient})
	if err != nil {
		t.Fatal(err)
	}
	dataPlane := startE2EDataPlane(t, ctx, remoteClient, gatewayClient, serverProfile, remoteSession)
	manager, err := clientpreview.NewManager(remoteClient, clientpreview.Config{TrafficStreams: dataPlane})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = manager.Shutdown(shutdownContext)
	})

	tcpTarget, tcpAddress := harness.StartLocalTCPEcho(t, "preview-tcp")
	defer tcpTarget.Close()
	udpTarget, udpAddress := harness.StartLocalUDPEcho(t, "preview-udp")
	defer udpTarget.Close()
	targets := []clientpreview.LocalTarget{
		{ServicePort: 8080, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: uint16(tcpAddress.Port)},
		{ServicePort: 9090, Protocol: "udp", LocalHost: "127.0.0.1", LocalPort: uint16(udpAddress.Port)},
	}

	// An existing user Service is a hard name conflict and must remain byte-for-
	// byte under its original UID instead of being adopted or overwritten.
	collisionName := previewName("preview-collision")
	collision, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: collisionName, Namespace: harness.EchoNamespace,
			Labels: map[string]string{"owner": "user"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "kubeloop-e2e-echo"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deletePreviewFixture(context.Background(), kubeClient, collisionName) })
	startContext, startCancel := context.WithTimeout(ctx, 30*time.Second)
	_, startErr := manager.Start(startContext, serverProfile, remoteSession, clientpreview.Request{
		ProfileID: serverProfile.ID, Namespace: remoteSession.Namespace, Name: collisionName, Targets: targets[:1],
	})
	startCancel()
	if startErr == nil {
		t.Fatal("Preview overwrote an existing user Service")
	}
	unchanged, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Get(ctx, collisionName, metav1.GetOptions{})
	if err != nil || unchanged.UID != collision.UID || unchanged.Labels["owner"] != "user" ||
		unchanged.Annotations["traffic.kubeloop.io/binding-name"] != "" ||
		unchanged.Annotations["traffic.kubeloop.io/binding-uid"] != "" {
		t.Fatalf("collision Service was changed: %#v err=%v", unchanged, err)
	}

	// Stop removes the exact owner Service/EndpointSlice while retaining a paused
	// TrafficBinding. Explicit delete then removes the durable recovery intent.
	firstName := previewName("preview-explicit")
	first := startRealPreview(t, ctx, manager, serverProfile, remoteSession, firstName, targets)
	assertPreviewOwned(t, ctx, kubeClient, stateStore, firstName, first.ID)
	assertTrafficBindingActive(ctx, t, bindingConfig, harness.EchoNamespace, first.ID, "Preview")
	harness.WaitClusterProbe(t, ctx, kubeClient, first.ClusterIP, 8080, "tcp", "explicit", "preview-tcp:")
	harness.WaitClusterProbe(t, ctx, kubeClient, first.ClusterIP, 9090, "udp", "explicit", "preview-udp:")
	pauseRealPreview(t, ctx, manager, serverProfile.ID, first.ID)
	waitForRealPreviewState(t, ctx, stateStore, first.ID, "stopped")
	assertTrafficBindingPaused(ctx, t, bindingConfig, harness.EchoNamespace, first.ID)
	assertPreviewResourcesAbsent(t, ctx, kubeClient, firstName)
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown Preview manager before restore: %v", err)
	}
	restoredManager, err := clientpreview.NewManager(
		remoteClient,
		clientpreview.Config{TrafficStreams: dataPlane},
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
		t.Fatalf("restore stopped Preview after client restart: %v", err)
	}
	restored := restoredManager.List(serverProfile.ID)
	if len(restored) != 1 || restored[0].ID != first.ID || restored[0].State != "paused" {
		t.Fatalf("restored Preview list = %#v", restored)
	}
	resumed, err := restoredManager.Resume(ctx, serverProfile.ID, first.ID)
	if err != nil {
		t.Fatalf("resume restored Preview: %v", err)
	}
	waitForRealPreviewState(t, ctx, stateStore, first.ID, "running")
	assertTrafficBindingActive(ctx, t, bindingConfig, harness.EchoNamespace, first.ID, "Preview")
	assertPreviewOwned(t, ctx, kubeClient, stateStore, firstName, first.ID)
	harness.WaitClusterProbe(t, ctx, kubeClient, resumed.ClusterIP, 8080, "tcp", "resumed", "preview-tcp:")
	pauseRealPreview(t, ctx, restoredManager, serverProfile.ID, first.ID)
	waitForRealPreviewState(t, ctx, stateStore, first.ID, "stopped")
	assertTrafficBindingPaused(ctx, t, bindingConfig, harness.EchoNamespace, first.ID)
	assertPreviewResourcesAbsent(t, ctx, kubeClient, firstName)
	deleteRealPreview(t, ctx, restoredManager, serverProfile.ID, first.ID)
	waitForRealPreviewState(t, ctx, stateStore, first.ID, "deleted")
	assertPreviewAbsent(t, ctx, kubeClient, bindingConfig, stateStore, firstName, first.ID)
	manager = restoredManager

	// Abrupt desktop loss sends neither a relay Stop frame nor a DELETE request.
	// The stream owner removes runtime resources but retains the paused binding so
	// startup recovery can discover it. Explicit deletion removes that intent.
	crashedName := previewName("preview-client-crash")
	crashed, err := remoteClient.CreatePreview(ctx, serverProfile, remoteSession, remote.PreviewSpec{
		Name: crashedName,
		Ports: []remote.PreviewPort{
			{ServicePort: 8080, Protocol: "tcp"},
			{ServicePort: 9090, Protocol: "udp"},
		},
	}, "preview-client-crash:"+uuid.NewString())
	if err != nil {
		t.Fatalf("create Preview for client crash: %v", err)
	}
	crashedConnection, err := dataPlane.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModePreview, crashed.ID)
	if err != nil {
		t.Fatalf("open Preview stream for client crash: %v", err)
	}
	waitForRealPreviewState(t, ctx, stateStore, crashed.ID, "running")
	assertPreviewOwned(t, ctx, kubeClient, stateStore, crashedName, crashed.ID)
	_ = crashedConnection.Close()
	waitForRealPreviewState(t, ctx, stateStore, crashed.ID, "failed")
	assertPreviewResourcesAbsent(t, ctx, kubeClient, crashedName)
	assertTrafficBindingPaused(ctx, t, bindingConfig, harness.EchoNamespace, crashed.ID)
	if err := bindings.Delete(ctx, harness.EchoNamespace, crashed.ID); err != nil {
		t.Fatalf("delete crashed Preview binding: %v", err)
	}
	assertTrafficBindingAbsent(ctx, t, bindingConfig, harness.EchoNamespace, crashed.ID)
	assertSnapshotCount(t, stateStore, crashed.ID, 0)

	// If a user takes ownership of the Service after creation, stop only removes
	// the still-owned EndpointSlice and preserves that Service. Deleting and
	// recreating the name is intentionally not used: an active Operator correctly
	// recreates a missing desired Service before a foreign create can win.
	replacedName := previewName("preview-replaced")
	replaced := startRealPreview(t, ctx, manager, serverProfile, remoteSession, replacedName, targets[:1])
	owned, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Get(ctx, replacedName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owned.OwnerReferences = nil
	owned.Annotations = nil
	owned.Labels = map[string]string{"owner": "replacement-user"}
	foreign, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Update(ctx, owned, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deletePreviewFixture(context.Background(), kubeClient, replacedName) })
	deleteRealPreview(t, ctx, manager, serverProfile.ID, replaced.ID)
	waitForRealPreviewState(t, ctx, stateStore, replaced.ID, "deleted")
	preserved, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Get(ctx, replacedName, metav1.GetOptions{})
	if err != nil || preserved.UID != foreign.UID || preserved.Labels["owner"] != "replacement-user" ||
		len(preserved.OwnerReferences) != 0 || preserved.Annotations["traffic.kubeloop.io/binding-uid"] != "" {
		t.Fatalf("user-owned Service was deleted or changed: %#v err=%v", preserved, err)
	}
	assertPreviewSliceAbsent(t, ctx, kubeClient, replacedName)
	assertTrafficBindingAbsent(ctx, t, bindingConfig, harness.EchoNamespace, replaced.ID)
	assertSnapshotCount(t, stateStore, replaced.ID, 0)

	// Simulate startup recovery reading the durable CRD directly. Pausing that
	// CRD must remove its owned resources without any database Task record.
	staleName := previewName("preview-stale")
	stale := startRealPreview(t, ctx, manager, serverProfile, remoteSession, staleName, targets)
	assertPreviewOwned(t, ctx, kubeClient, stateStore, staleName, stale.ID)
	assertSnapshotCount(t, stateStore, stale.ID, 0)
	if err := bindings.Pause(ctx, harness.EchoNamespace, stale.ID); err != nil {
		t.Fatalf("recover real Preview from its TrafficBinding: %v", err)
	}
	waitForRealPreviewState(t, ctx, stateStore, stale.ID, "stopped")
	if err := bindings.Delete(ctx, harness.EchoNamespace, stale.ID); err != nil {
		t.Fatalf("delete recovered Preview: %v", err)
	}
	assertPreviewAbsent(t, ctx, kubeClient, bindingConfig, stateStore, staleName, stale.ID)
}

func previewLifecycleState(
	t *testing.T,
	ctx context.Context,
	serviceIP string,
) (*storage.Store, controlplaneapi.Identity, sessionapi.ActiveSession, remote.Session) {
	t.Helper()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "preview-lifecycle.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	identityID, authorizationID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	deviceID := "preview-e2e-device"
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(10 * time.Minute)
	createOAuthGrant(t, ctx, stateStore, authorizationID, identityID, deviceID, 9, now, expiresAt)
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

func startPreviewLifecycleController(
	t *testing.T,
	handler *previewapi.Service,
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
					if request.Header.Get("Authorization") != "Bearer "+previewLifecycleAccessToken {
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
			Previews: previewapi.NewRoutes(handler).Endpoints(),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return startE2EControlPlaneServer(t, server.Handler(), gateway)
}

func startRealPreview(
	t *testing.T,
	ctx context.Context,
	manager *clientpreview.Manager,
	serverProfile profile.Profile,
	session remote.Session,
	name string,
	targets []clientpreview.LocalTarget,
) clientpreview.Info {
	t.Helper()
	startContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := manager.Start(startContext, serverProfile, session, clientpreview.Request{
		ProfileID: serverProfile.ID, Namespace: session.Namespace, Name: name, Targets: targets,
	})
	if err != nil {
		t.Fatalf("start real Preview: %v", err)
	}
	return info
}

func pauseRealPreview(
	t *testing.T,
	ctx context.Context,
	manager *clientpreview.Manager,
	profileID, taskID string,
) {
	t.Helper()
	stopContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := manager.Pause(stopContext, profileID, taskID); err != nil {
		t.Fatalf("pause real Preview: %v", err)
	}
}

func deleteRealPreview(
	t *testing.T,
	ctx context.Context,
	manager *clientpreview.Manager,
	profileID, taskID string,
) {
	t.Helper()
	deleteContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := manager.Delete(deleteContext, profileID, taskID); err != nil {
		t.Fatalf("delete real Preview: %v", err)
	}
}

func previewName(prefix string) string {
	return prefix + "-" + strings.ToLower(uuid.NewString()[:8])
}

func assertPreviewOwned(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	stateStore *storage.Store,
	name, taskID string,
) {
	t.Helper()
	bindingName := "kubeloop-" + taskID
	service, err := client.CoreV1().Services(harness.EchoNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil || service.Annotations["traffic.kubeloop.io/binding-name"] != bindingName ||
		service.Annotations["traffic.kubeloop.io/binding-uid"] == "" ||
		service.Annotations["traffic.kubeloop.io/mode"] != "Preview" ||
		service.Labels["app.kubernetes.io/managed-by"] != "kubeloop-operator" ||
		!previewOwnedByBinding(
			service.OwnerReferences,
			bindingName,
			service.Annotations["traffic.kubeloop.io/binding-uid"],
		) {
		t.Fatalf("owned Preview Service=%#v err=%v", service, err)
	}
	slices, err := previewSlices(ctx, client, name)
	if err != nil || len(slices) != 1 ||
		slices[0].Annotations["traffic.kubeloop.io/binding-name"] != bindingName ||
		slices[0].Annotations["traffic.kubeloop.io/binding-uid"] != service.Annotations["traffic.kubeloop.io/binding-uid"] ||
		!previewOwnedByBinding(
			slices[0].OwnerReferences,
			bindingName,
			service.Annotations["traffic.kubeloop.io/binding-uid"],
		) {
		t.Fatalf("owned Preview EndpointSlices=%#v err=%v", slices, err)
	}
	assertSnapshotCount(t, stateStore, taskID, 0)
}

func previewOwnedByBinding(references []metav1.OwnerReference, name, uid string) bool {
	for _, reference := range references {
		if reference.APIVersion == "traffic.kubeloop.io/v1alpha1" && reference.Kind == "TrafficBinding" &&
			reference.Name == name && string(reference.UID) == uid && reference.Controller != nil && *reference.Controller {
			return true
		}
	}
	return false
}

func previewSlices(ctx context.Context, client kubernetes.Interface, name string) ([]discoveryv1.EndpointSlice, error) {
	list, err := client.DiscoveryV1().EndpointSlices(harness.EchoNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: servicebinding.ServiceNameLabel + "=" + name,
	})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func assertPreviewAbsent(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	bindingConfig *rest.Config,
	stateStore *storage.Store,
	name, taskID string,
) {
	t.Helper()
	assertPreviewResourcesAbsent(t, ctx, client, name)
	assertTrafficBindingAbsent(ctx, t, bindingConfig, harness.EchoNamespace, taskID)
	assertSnapshotCount(t, stateStore, taskID, 0)
}

func assertPreviewResourcesAbsent(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	name string,
) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, serviceErr := client.CoreV1().Services(harness.EchoNamespace).Get(ctx, name, metav1.GetOptions{})
		slices, sliceErr := previewSlices(ctx, client, name)
		if apierrors.IsNotFound(serviceErr) && sliceErr == nil && len(slices) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf(
				"Preview resources %s were not deleted: service=%v slices=%#v error=%v",
				name,
				serviceErr,
				slices,
				sliceErr,
			)
		case <-ticker.C:
		}
	}
}

func assertTrafficBindingActive(
	ctx context.Context,
	t *testing.T,
	config *rest.Config,
	namespace, taskID, mode string,
) {
	t.Helper()
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	name, err := trafficbindingclient.NameForTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	resource := client.Resource(schema.GroupVersionResource{
		Group: "traffic.kubeloop.io", Version: "v1alpha1", Resource: "trafficbindings",
	}).Namespace(namespace)
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		binding, getErr := resource.Get(ctx, name, metav1.GetOptions{})
		if getErr == nil {
			desiredState, _, _ := unstructured.NestedString(binding.Object, "spec", "desiredState")
			bindingMode, _, _ := unstructured.NestedString(binding.Object, "spec", "mode")
			phase, _, _ := unstructured.NestedString(binding.Object, "status", "phase")
			if desiredState == "Active" && bindingMode == mode && phase == "Ready" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("TrafficBinding %s was not active in %s mode: %v", name, mode, getErr)
		case <-ticker.C:
		}
	}
}

func assertTrafficBindingPaused(
	ctx context.Context,
	t *testing.T,
	config *rest.Config,
	namespace, taskID string,
) {
	t.Helper()
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	name, err := trafficbindingclient.NameForTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	resource := client.Resource(schema.GroupVersionResource{
		Group: "traffic.kubeloop.io", Version: "v1alpha1", Resource: "trafficbindings",
	}).Namespace(namespace)
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		binding, getErr := resource.Get(ctx, name, metav1.GetOptions{})
		if getErr == nil {
			desiredState, _, _ := unstructured.NestedString(binding.Object, "spec", "desiredState")
			phase, _, _ := unstructured.NestedString(binding.Object, "status", "phase")
			if desiredState == "Paused" && phase == "Paused" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("TrafficBinding %s was not paused: %v", name, getErr)
		case <-ticker.C:
		}
	}
}

func assertTrafficBindingAbsent(
	ctx context.Context,
	t *testing.T,
	config *rest.Config,
	namespace, taskID string,
) {
	t.Helper()
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	name, err := trafficbindingclient.NameForTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	resource := client.Resource(schema.GroupVersionResource{
		Group: "traffic.kubeloop.io", Version: "v1alpha1", Resource: "trafficbindings",
	}).Namespace(namespace)
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err = resource.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("TrafficBinding %s was not deleted: %v", name, err)
		case <-ticker.C:
		}
	}
}

func assertPreviewSliceAbsent(t *testing.T, ctx context.Context, client kubernetes.Interface, name string) {
	t.Helper()
	slices, err := previewSlices(ctx, client, name)
	if err != nil || len(slices) != 0 {
		t.Fatalf("owned Preview EndpointSlice still exists for %s: slices=%#v err=%v", name, slices, err)
	}
}

func deletePreviewFixture(ctx context.Context, client kubernetes.Interface, name string) {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = client.CoreV1().Services(harness.EchoNamespace).Delete(cleanupContext, name, metav1.DeleteOptions{})
	if slices, err := previewSlices(cleanupContext, client, name); err == nil {
		for _, slice := range slices {
			_ = client.DiscoveryV1().EndpointSlices(harness.EchoNamespace).Delete(
				cleanupContext, slice.Name, metav1.DeleteOptions{},
			)
		}
	}
}

func waitForRealPreviewState(
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
			t.Fatalf("Preview Session %s did not reach %s: state=%s err=%v", taskID, want, state, getErr)
		case <-ticker.C:
		}
	}
}
