//go:build e2e

package dataplane

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	stateStore, principal, activeSession, remoteSession := previewLifecycleState(
		t, ctx, harness.EchoServiceIP(t, ctx, kubeClient),
	)
	kubeOutage := &temporaryKubernetesOutage{}
	providerConfig := kubeRESTConfig(t)
	providerConfig.WrapTransport = kubeOutage.WrapTransport
	provider, err := controlplanekubernetes.NewForRESTConfig(providerConfig, controlplanekubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	bindingConfig, err := provider.SystemRESTConfig()
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := trafficbindingclient.NewForRESTConfig(bindingConfig, trafficbindingclient.Config{})
	if err != nil {
		t.Fatal(err)
	}
	realResources, err := previewapi.NewTrafficBindingResourceManager(stateStore, bindings)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := previewapi.New(
		stateStore,
		e2eExecSessionValidator{principalID: principal.Subject, session: activeSession},
		realResources,
		previewapi.Config{
			DeleteTimeout: 5 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := startPreviewLifecycleController(t, handler, principal)
	defer server.Close()

	serverProfile := profile.Profile{ID: "preview-e2e", BaseURL: server.URL}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: previewLifecycleAccessToken,
			AccessExpiresAt: principal.AccessExpiresAt, RefreshToken: "unused",
			RefreshExpiresAt: principal.AccessExpiresAt, DeviceID: principal.DeviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := clientpreview.NewManager(remoteClient, clientpreview.Config{})
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
		ProfileID: serverProfile.ID, Name: collisionName, Targets: targets[:1],
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

	// Explicit stop removes the exact owner Service/EndpointSlice and leaves no
	// cleanup intent after real cluster TCP and UDP traffic reached the desktop.
	firstName := previewName("preview-explicit")
	first := startRealPreview(t, ctx, manager, serverProfile, remoteSession, firstName, targets)
	assertPreviewOwned(t, ctx, kubeClient, stateStore, firstName, first.ID)
	harness.WaitClusterProbe(t, ctx, kubeClient, first.ClusterIP, 8080, "tcp", "explicit", "preview-tcp:")
	harness.WaitClusterProbe(t, ctx, kubeClient, first.ClusterIP, 9090, "udp", "explicit", "preview-udp:")
	stopRealPreview(t, ctx, manager, serverProfile.ID, first.ID)
	waitForRealPreviewState(t, ctx, stateStore, first.ID, "stopped")
	assertPreviewAbsent(t, ctx, kubeClient, bindingConfig, stateStore, firstName, first.ID)

	// Abrupt desktop loss sends neither a relay Stop frame nor a DELETE request.
	// The stream owner must still delete the exact-owner Service and EndpointSlice.
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
	crashedConnection, err := remoteClient.OpenPreviewStream(ctx, serverProfile, remoteSession, crashed)
	if err != nil {
		t.Fatalf("open Preview stream for client crash: %v", err)
	}
	waitForRealPreviewState(t, ctx, stateStore, crashed.ID, "running")
	assertPreviewOwned(t, ctx, kubeClient, stateStore, crashedName, crashed.ID)
	crashedConnection.CloseNow()
	waitForRealPreviewState(t, ctx, stateStore, crashed.ID, "failed")
	assertPreviewAbsent(t, ctx, kubeClient, bindingConfig, stateStore, crashedName, crashed.ID)

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
	stopRealPreview(t, ctx, manager, serverProfile.ID, replaced.ID)
	waitForRealPreviewState(t, ctx, stateStore, replaced.ID, "stopped")
	preserved, err := kubeClient.CoreV1().Services(harness.EchoNamespace).Get(ctx, replacedName, metav1.GetOptions{})
	if err != nil || preserved.UID != foreign.UID || preserved.Labels["owner"] != "replacement-user" ||
		len(preserved.OwnerReferences) != 0 || preserved.Annotations["traffic.kubeloop.io/binding-uid"] != "" {
		t.Fatalf("user-owned Service was deleted or changed: %#v err=%v", preserved, err)
	}
	assertPreviewSliceAbsent(t, ctx, kubeClient, replacedName)
	assertTrafficBindingAbsent(t, ctx, bindingConfig, replaced.ID)
	assertSnapshotCount(t, stateStore, replaced.ID, 0)

	// A real client-go 503 outage leaves durable recovery intent. After the API
	// returns, a replacement worker uses its system client to delete only the
	// resources carrying the Task UUID owner.
	staleName := previewName("preview-stale")
	stale := startRealPreview(t, ctx, manager, serverProfile, remoteSession, staleName, targets)
	assertPreviewOwned(t, ctx, kubeClient, stateStore, staleName, stale.ID)
	kubeOutage.Enable()
	stopRealPreview(t, ctx, manager, serverProfile.ID, stale.ID)
	waitForRealPreviewState(t, ctx, stateStore, stale.ID, "recovering")
	if requests := kubeOutage.RequestCount(); requests == 0 {
		t.Fatal("Preview cleanup did not exercise the simulated Kubernetes API outage")
	}
	assertSnapshotCount(t, stateStore, stale.ID, 0)
	kubeOutage.Disable()
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
	if recovered, recoverErr := reconciler.RunOnce(ctx); recoverErr != nil || recovered != 1 {
		t.Fatalf("recover stale real Preview: recovered=%d err=%v", recovered, recoverErr)
	}
	waitForRealPreviewState(t, ctx, stateStore, stale.ID, "failed")
	assertPreviewAbsent(t, ctx, kubeClient, bindingConfig, stateStore, staleName, stale.ID)

	// Token Family revocation ends the owner lease and deletes resources even
	// when the desktop sends no explicit stop request.
	revokedName := previewName("preview-revoked")
	revoked := startRealPreview(t, ctx, manager, serverProfile, remoteSession, revokedName, targets)
	harness.WaitClusterProbe(t, ctx, kubeClient, revoked.ClusterIP, 8080, "tcp", "before-revoke", "preview-tcp:")
	if err := stateStore.TokenFamilies().Revoke(ctx, principal.FamilyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	waitForRealPreviewState(t, ctx, stateStore, revoked.ID, "stopped")
	waitForNoLocalPreview(t, ctx, manager, serverProfile.ID)
	assertPreviewAbsent(t, ctx, kubeClient, bindingConfig, stateStore, revokedName, revoked.ID)
}

func previewLifecycleState(
	t *testing.T,
	ctx context.Context,
	serviceIP string,
) (*storage.Store, controlplaneapi.Principal, sessionapi.ActiveSession, remote.Session) {
	t.Helper()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "preview-lifecycle.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, familyID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	deviceID := "preview-e2e-device"
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "e2e", ExternalID: "preview-lifecycle",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(10 * time.Minute)
	if err := stateStore.TokenFamilies().Create(ctx, storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: deviceID,
		RefreshTokenHash: bytes.Repeat([]byte{9}, 32), CreatedAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{serviceIP}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	principal := controlplaneapi.Principal{
		Subject: principalID, DeviceID: deviceID, FamilyID: familyID, AccessExpiresAt: expiresAt,
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
	return stateStore, principal, active, clientSession
}

func startPreviewLifecycleController(
	t *testing.T,
	handler *previewapi.Service,
	principal controlplaneapi.Principal,
) *httptest.Server {
	t.Helper()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "e2e-preview", Subjects: []string{principal.Subject},
		Namespaces: []string{harness.EchoNamespace},
		Operations: []string{"create", "get", "delete", "stream"}, ResourceKinds: []string{"previews"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://127.0.0.1"}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(request *http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
			if request.Header.Get("Authorization") != "Bearer "+previewLifecycleAccessToken {
				return controlplaneapi.Principal{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "invalid e2e access token"}
			}
			return principal, nil
		})),
		controlplane.WithAuthorizer(policy), controlplane.WithAPIRoutes(controlplane.APIRoutes{Previews: previewapi.NewRoutes(handler).Endpoints()}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(server.Handler())
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
		ProfileID: serverProfile.ID, Name: name, Targets: targets,
	})
	if err != nil {
		t.Fatalf("start real Preview: %v", err)
	}
	return info
}

func stopRealPreview(
	t *testing.T,
	ctx context.Context,
	manager *clientpreview.Manager,
	profileID, taskID string,
) {
	t.Helper()
	stopContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := manager.Stop(stopContext, profileID, taskID); err != nil {
		t.Fatalf("stop real Preview: %v", err)
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
		!previewOwnedByBinding(service.OwnerReferences, bindingName, service.Annotations["traffic.kubeloop.io/binding-uid"]) {
		t.Fatalf("owned Preview Service=%#v err=%v", service, err)
	}
	slices, err := previewSlices(ctx, client, name)
	if err != nil || len(slices) != 1 ||
		slices[0].Annotations["traffic.kubeloop.io/binding-name"] != bindingName ||
		slices[0].Annotations["traffic.kubeloop.io/binding-uid"] != service.Annotations["traffic.kubeloop.io/binding-uid"] ||
		!previewOwnedByBinding(slices[0].OwnerReferences, bindingName, service.Annotations["traffic.kubeloop.io/binding-uid"]) {
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
			t.Fatalf("Preview resources %s were not deleted: service=%v slices=%#v error=%v", name, serviceErr, slices, sliceErr)
		case <-ticker.C:
		}
	}
	assertTrafficBindingAbsent(t, ctx, bindingConfig, taskID)
	assertSnapshotCount(t, stateStore, taskID, 0)
}

func assertTrafficBindingAbsent(t *testing.T, ctx context.Context, config *rest.Config, taskID string) {
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
	}).Namespace(harness.EchoNamespace)
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
			t.Fatalf("Preview Task %s did not reach %s: task=%#v err=%v", taskID, want, task, err)
		case <-ticker.C:
		}
	}
}

func waitForNoLocalPreview(
	t *testing.T,
	ctx context.Context,
	manager *clientpreview.Manager,
	profileID string,
) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if len(manager.List(profileID)) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("local Preview relay did not stop after Token Family revocation")
		case <-ticker.C:
		}
	}
}
