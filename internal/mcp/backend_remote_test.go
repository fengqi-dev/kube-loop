package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/gorilla/websocket"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type fakeProfiles struct{ state clientprofile.State }

func (profiles fakeProfiles) Snapshot() clientprofile.State { return profiles.state }

type fakeControlPlane struct {
	versionCalls          int
	profileID             string
	capabilitiesNamespace string
	podsNamespace         string
	servicesNamespace     string
	podFile               clientremote.PodFileSpec
	podFileKey            string
}

func (gateway *fakeControlPlane) Version(
	_ context.Context,
	profile clientprofile.Profile,
) (clientremote.Version, error) {
	gateway.versionCalls++
	gateway.profileID = profile.ID
	return clientremote.Version{GitVersion: "v1.32.0"}, nil
}

func (gateway *fakeControlPlane) Capabilities(
	_ context.Context,
	_ clientprofile.Profile,
	namespace string,
) (clientremote.Capabilities, error) {
	gateway.capabilitiesNamespace = namespace
	return clientremote.Capabilities{Namespace: namespace}, nil
}
func (*fakeControlPlane) Namespaces(context.Context, clientprofile.Profile) ([]clientremote.Namespace, error) {
	return []clientremote.Namespace{{Name: "default"}}, nil
}
func (gateway *fakeControlPlane) Pods(
	_ context.Context,
	_ clientprofile.Profile,
	namespace string,
) ([]clientremote.Pod, error) {
	gateway.podsNamespace = namespace
	return []clientremote.Pod{{Name: "api-0", Namespace: namespace}}, nil
}
func (gateway *fakeControlPlane) Services(
	_ context.Context,
	_ clientprofile.Profile,
	namespace string,
) ([]clientremote.Service, error) {
	gateway.servicesNamespace = namespace
	return []clientremote.Service{{Name: "api", Namespace: namespace}}, nil
}
func (gateway *fakeControlPlane) ListPodFiles(
	_ context.Context,
	_ clientprofile.Profile,
	session clientremote.Session,
	spec clientremote.PodFileSpec,
) (clientremote.PodFileList, error) {
	gateway.podFile = spec
	return clientremote.PodFileList{
		SessionID: session.ID, Namespace: session.Namespace, Pod: spec.Pod,
		Container: spec.Container, Path: spec.Path, Items: []clientremote.PodFileEntry{},
	}, nil
}
func (gateway *fakeControlPlane) CreatePodFileOperation(
	_ context.Context,
	_ clientprofile.Profile,
	session clientremote.Session,
	action string,
	spec clientremote.PodFileSpec,
	idempotencyKey string,
) (clientremote.PodFileTask, error) {
	gateway.podFile, gateway.podFileKey = spec, idempotencyKey
	return clientremote.PodFileTask{
		ID: "file-op-1", SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Action: action, Pod: spec.Pod, Path: spec.Path,
		Result: clientremote.PodFileResult{Completed: true},
	}, nil
}

type fakeSessions struct {
	current         clientremote.Session
	disconnectCalls int
}

func (sessions *fakeSessions) Connect(
	_ context.Context,
	_ clientprofile.Profile,
	namespace string,
) (clientremote.Session, error) {
	sessions.current.Namespace, sessions.current.State = namespace, "active"
	return sessions.current, nil
}
func (sessions *fakeSessions) Current(string) (clientremote.Session, error) {
	if sessions.current.ID == "" {
		return clientremote.Session{}, errors.New("not connected")
	}
	return sessions.current, nil
}
func (sessions *fakeSessions) Disconnect(context.Context, string) error {
	sessions.disconnectCalls++
	return nil
}

type fakeDataPlanes struct {
	connectCalls    *int
	disconnectCalls *int
}

func (dataPlanes fakeDataPlanes) Connect(
	context.Context,
	clientprofile.Profile,
	clientremote.Session,
) (clientdataplane.Status, error) {
	if dataPlanes.connectCalls != nil {
		*dataPlanes.connectCalls++
	}
	return clientdataplane.Status{}, nil
}
func (dataPlanes fakeDataPlanes) Disconnect(string) error {
	if dataPlanes.disconnectCalls != nil {
		*dataPlanes.disconnectCalls++
	}
	return nil
}

type fakeExchangeManager struct {
	items     []clientexchange.Info
	started   clientexchange.Request
	stoppedID string
}

func (manager *fakeExchangeManager) Start(
	_ context.Context,
	_ clientprofile.Profile,
	_ clientremote.Session,
	request clientexchange.Request,
) (clientexchange.Info, error) {
	manager.started = request
	return manager.items[0], nil
}
func (manager *fakeExchangeManager) Stop(_ context.Context, _ string, id string) error {
	manager.stoppedID = id
	return nil
}
func (manager *fakeExchangeManager) Pause(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakeExchangeManager) Resume(_ context.Context, _ string, id string) (clientexchange.Info, error) {
	for _, item := range manager.items {
		if item.ID == id {
			return item, nil
		}
	}
	return clientexchange.Info{}, nil
}
func (manager *fakeExchangeManager) Delete(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakeExchangeManager) List(string) []clientexchange.Info { return manager.items }
func (*fakeExchangeManager) StopProfile(context.Context, string) error { return nil }

type fakeMirrorManager struct {
	items     []clientmirror.Info
	started   clientmirror.Request
	stoppedID string
}

func (manager *fakeMirrorManager) Start(
	_ context.Context,
	_ clientprofile.Profile,
	_ clientremote.Session,
	request clientmirror.Request,
) (clientmirror.Info, error) {
	manager.started = request
	return manager.items[0], nil
}
func (manager *fakeMirrorManager) Stop(_ context.Context, _ string, id string) error {
	manager.stoppedID = id
	return nil
}
func (manager *fakeMirrorManager) Pause(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakeMirrorManager) Resume(_ context.Context, _ string, id string) (clientmirror.Info, error) {
	for _, item := range manager.items {
		if item.ID == id {
			return item, nil
		}
	}
	return clientmirror.Info{}, nil
}
func (manager *fakeMirrorManager) Delete(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakeMirrorManager) List(string) []clientmirror.Info   { return manager.items }
func (*fakeMirrorManager) StopProfile(context.Context, string) error { return nil }

type fakePreviewManager struct {
	items     []clientpreview.Info
	started   clientpreview.Request
	stoppedID string
}

func (manager *fakePreviewManager) Start(
	_ context.Context,
	_ clientprofile.Profile,
	_ clientremote.Session,
	request clientpreview.Request,
) (clientpreview.Info, error) {
	manager.started = request
	return manager.items[0], nil
}
func (manager *fakePreviewManager) Stop(_ context.Context, _ string, id string) error {
	manager.stoppedID = id
	return nil
}
func (manager *fakePreviewManager) Pause(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakePreviewManager) Resume(_ context.Context, _ string, id string) (clientpreview.Info, error) {
	for _, item := range manager.items {
		if item.ID == id {
			return item, nil
		}
	}
	return clientpreview.Info{}, nil
}
func (manager *fakePreviewManager) Delete(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakePreviewManager) List(string) []clientpreview.Info  { return manager.items }
func (*fakePreviewManager) StopProfile(context.Context, string) error { return nil }

type fakeForwardManager struct {
	items     []clientportforward.Info
	started   clientportforward.Request
	stoppedID string
}

func (manager *fakeForwardManager) Start(
	_ context.Context,
	_ clientprofile.Profile,
	_ clientremote.Session,
	request clientportforward.Request,
) (clientportforward.Info, error) {
	manager.started = request
	return manager.items[0], nil
}
func (manager *fakeForwardManager) Stop(_ context.Context, _ string, id string) error {
	manager.stoppedID = id
	return nil
}
func (manager *fakeForwardManager) Pause(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakeForwardManager) Resume(
	_ context.Context,
	_ string,
	id string,
) (clientportforward.Info, error) {
	for _, item := range manager.items {
		if item.ID == id {
			return item, nil
		}
	}
	return clientportforward.Info{}, nil
}
func (manager *fakeForwardManager) Delete(ctx context.Context, profileID, id string) error {
	return manager.Stop(ctx, profileID, id)
}
func (manager *fakeForwardManager) List(string) []clientportforward.Info { return manager.items }
func (*fakeForwardManager) StopProfile(context.Context, string) error    { return nil }

type fakeExecClient struct{}

var _ clientexec.Client = fakeExecClient{}

func (fakeExecClient) CreateExecTask(
	context.Context,
	clientprofile.Profile,
	clientremote.Session,
	clientremote.ExecSpec,
	string,
) (clientremote.ExecTask, error) {
	return clientremote.ExecTask{}, errors.New("not implemented")
}

func (fakeExecClient) OpenExecStream(
	context.Context,
	clientprofile.Profile,
	clientremote.Session,
	clientremote.ExecTask,
) (*websocket.Conn, error) {
	return nil, errors.New("not implemented")
}

func TestNewRemoteBackendRequiresCoreDependencies(t *testing.T) {
	valid := func() RemoteDependencies {
		return RemoteDependencies{
			Profiles: fakeProfiles{state: clientprofile.State{
				ActiveProfileID: "active",
				Profiles:        []clientprofile.Profile{{ID: "active"}},
			}},
			ControlPlane: &fakeControlPlane{},
			Sessions:     &fakeSessions{},
			DataPlanes:   fakeDataPlanes{},
			ExecClient:   fakeExecClient{},
		}
	}
	tests := []struct {
		name   string
		mutate func(*RemoteDependencies)
	}{
		{name: "profiles", mutate: func(dependencies *RemoteDependencies) { dependencies.Profiles = nil }},
		{
			name:   "control plane",
			mutate: func(dependencies *RemoteDependencies) { dependencies.ControlPlane = nil },
		},
		{name: "sessions", mutate: func(dependencies *RemoteDependencies) { dependencies.Sessions = nil }},
		{name: "data planes", mutate: func(dependencies *RemoteDependencies) { dependencies.DataPlanes = nil }},
		{name: "exec client", mutate: func(dependencies *RemoteDependencies) { dependencies.ExecClient = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := valid()
			test.mutate(&dependencies)
			if _, err := NewRemoteBackend(dependencies); err == nil {
				t.Fatal("NewRemoteBackend accepted incomplete dependencies")
			}
		})
	}
	if _, err := NewRemoteBackend(valid()); err != nil {
		t.Fatalf("NewRemoteBackend rejected complete dependencies: %v", err)
	}
}

func TestRemoteBackendDelegatesInventoryForActiveProfile(t *testing.T) {
	gateway := &fakeControlPlane{}
	sessions := &fakeSessions{current: clientremote.Session{
		ID: "session-a", Namespace: "default", State: sessionStateActive,
	}}
	backend, err := NewRemoteBackend(RemoteDependencies{
		Profiles: fakeProfiles{state: clientprofile.State{
			ActiveProfileID: "active", Profiles: []clientprofile.Profile{{ID: "active"}},
		}},
		ControlPlane: gateway,
		Sessions:     sessions,
		DataPlanes:   fakeDataPlanes{},
		ExecClient:   fakeExecClient{},
	})
	if err != nil {
		t.Fatal(err)
	}

	capabilities, err := backend.Capabilities(t.Context(), "active", " default ")
	if err != nil || capabilities.Namespace != "default" || gateway.capabilitiesNamespace != "default" {
		t.Fatalf("capabilities=%#v namespace=%q error=%v", capabilities, gateway.capabilitiesNamespace, err)
	}
	namespaces, err := backend.Namespaces(t.Context(), "active")
	if err != nil || len(namespaces) != 1 || namespaces[0].Name != "default" {
		t.Fatalf("namespaces=%#v error=%v", namespaces, err)
	}
	pods, err := backend.Pods(t.Context(), "active", " default ")
	if err != nil || len(pods) != 1 || gateway.podsNamespace != "default" {
		t.Fatalf("pods=%#v namespace=%q error=%v", pods, gateway.podsNamespace, err)
	}
	services, err := backend.Services(t.Context(), "active", " default ")
	if err != nil || len(services) != 1 || gateway.servicesNamespace != "default" {
		t.Fatalf("services=%#v namespace=%q error=%v", services, gateway.servicesNamespace, err)
	}
	current, err := backend.CurrentSession("active")
	if err != nil || current.ID != "session-a" {
		t.Fatalf("current=%#v error=%v", current, err)
	}
}

func TestRemoteBackendConnectSwitchesNamespaceAndStartsDataPlane(t *testing.T) {
	connectCalls, disconnectCalls := 0, 0
	sessions := &fakeSessions{current: clientremote.Session{
		ID: "session-a", Namespace: "old", State: sessionStateActive,
	}}
	backend, err := NewRemoteBackend(RemoteDependencies{
		Profiles: fakeProfiles{state: clientprofile.State{
			ActiveProfileID: "active", Profiles: []clientprofile.Profile{{ID: "active"}},
		}},
		ControlPlane: &fakeControlPlane{},
		Sessions:     sessions,
		DataPlanes: fakeDataPlanes{
			connectCalls: &connectCalls, disconnectCalls: &disconnectCalls,
		},
		ExecClient: fakeExecClient{},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := backend.Connect(t.Context(), "active", "   "); err == nil {
		t.Fatal("Connect accepted an empty namespace")
	}
	session, err := backend.Connect(t.Context(), "active", " new ")
	if err != nil {
		t.Fatal(err)
	}
	if session.Namespace != "new" || connectCalls != 1 || disconnectCalls != 1 {
		t.Fatalf(
			"session=%#v connectCalls=%d disconnectCalls=%d",
			session,
			connectCalls,
			disconnectCalls,
		)
	}
}

func TestRemoteBackendTrafficLifecycle(t *testing.T) {
	const profileID, sessionID, namespace = "active", "session-a", "default"
	exchanges := &fakeExchangeManager{items: []clientexchange.Info{{
		ID: "d", ProfileID: profileID, SessionID: sessionID, Namespace: namespace,
	}}}
	mirrors := &fakeMirrorManager{items: []clientmirror.Info{{
		ID: "c", ProfileID: profileID, SessionID: sessionID, Namespace: namespace,
	}}}
	previews := &fakePreviewManager{items: []clientpreview.Info{{
		ID: "b", ProfileID: profileID, SessionID: sessionID, Namespace: namespace,
	}}}
	forwards := &fakeForwardManager{items: []clientportforward.Info{{
		ID: "a", ProfileID: profileID, SessionID: sessionID, Namespace: namespace,
	}}}
	backend, err := NewRemoteBackend(RemoteDependencies{
		Profiles: fakeProfiles{state: clientprofile.State{
			ActiveProfileID: profileID, Profiles: []clientprofile.Profile{{ID: profileID}},
		}},
		ControlPlane: &fakeControlPlane{},
		Sessions: &fakeSessions{current: clientremote.Session{
			ID: sessionID, Namespace: namespace, State: sessionStateActive,
		}},
		DataPlanes: fakeDataPlanes{}, ExecClient: fakeExecClient{},
		Exchanges: exchanges, Mirrors: mirrors, Previews: previews, Forwards: forwards,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		trafficType string
		request     TrafficStartRequest
		wantID      string
	}{
		{
			name: "exchange", trafficType: trafficTypeExchange, wantID: "d",
			request: TrafficStartRequest{Service: "api", Targets: []LocalTarget{{ServicePort: 80}}},
		},
		{
			name: "mirror", trafficType: trafficTypeMirror, wantID: "c",
			request: TrafficStartRequest{Service: "api", Targets: []LocalTarget{{ServicePort: 80}}},
		},
		{
			name: "preview", trafficType: trafficTypePreview, wantID: "b",
			request: TrafficStartRequest{Name: "local-api", Targets: []LocalTarget{{ServicePort: 80}}},
		},
		{
			name: "port forward", trafficType: trafficTypePortForward, wantID: "a",
			request: TrafficStartRequest{
				TargetKind: "pod", TargetName: "api-0", Protocol: "tcp", RemotePort: 8080,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := test.request
			request.Type, request.ProfileID = test.trafficType, profileID
			request.SessionID, request.Namespace = sessionID, namespace
			item, err := backend.StartTraffic(t.Context(), request)
			if err != nil || item.Type != test.trafficType || trafficItemID(item) != test.wantID {
				t.Fatalf("item=%#v error=%v", item, err)
			}
		})
	}

	items, err := backend.ListTraffic(profileID, "")
	if err != nil || len(items) != 4 {
		t.Fatalf("items=%#v error=%v", items, err)
	}
	for index, wantID := range []string{"a", "b", "c", "d"} {
		if got := trafficItemID(items[index]); got != wantID {
			t.Fatalf("items[%d] ID = %q, want %q", index, got, wantID)
		}
	}

	for _, identity := range []TrafficIdentity{
		{Type: trafficTypeExchange, ProfileID: profileID, SessionID: sessionID, Namespace: namespace, TaskID: "d"},
		{Type: trafficTypeMirror, ProfileID: profileID, SessionID: sessionID, Namespace: namespace, TaskID: "c"},
		{Type: trafficTypePreview, ProfileID: profileID, SessionID: sessionID, Namespace: namespace, TaskID: "b"},
		{Type: trafficTypePortForward, ProfileID: profileID, SessionID: sessionID, Namespace: namespace, TaskID: "a"},
	} {
		if err := backend.PauseTraffic(t.Context(), identity); err != nil {
			t.Fatalf("PauseTraffic(%q): %v", identity.Type, err)
		}
		item, err := backend.ResumeTraffic(t.Context(), identity)
		if err != nil || trafficItemID(item) != identity.TaskID {
			t.Fatalf("ResumeTraffic(%q): item=%#v error=%v", identity.Type, item, err)
		}
		if err := backend.DeleteTraffic(t.Context(), identity); err != nil {
			t.Fatalf("DeleteTraffic(%q): %v", identity.Type, err)
		}
	}
	if exchanges.stoppedID != "d" || mirrors.stoppedID != "c" || previews.stoppedID != "b" ||
		forwards.stoppedID != "a" {
		t.Fatalf(
			"stopped exchange=%q mirror=%q preview=%q forward=%q",
			exchanges.stoppedID,
			mirrors.stoppedID,
			previews.stoppedID,
			forwards.stoppedID,
		)
	}
}

func TestRemoteBackendRejectsNonActiveProfileBeforeControlPlaneCall(t *testing.T) {
	gateway := &fakeControlPlane{}
	backend, err := NewRemoteBackend(RemoteDependencies{
		Profiles: fakeProfiles{state: clientprofile.State{
			ActiveProfileID: "active", Profiles: []clientprofile.Profile{{ID: "active"}, {ID: "other"}},
		}},
		ControlPlane: gateway, Sessions: &fakeSessions{}, DataPlanes: fakeDataPlanes{}, ExecClient: fakeExecClient{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Version(context.Background(), "other")
	assertToolError(t, err, ErrorForbidden, "profileId")
	if gateway.versionCalls != 0 {
		t.Fatalf("non-active Profile reached Control Plane: %d calls", gateway.versionCalls)
	}
	version, err := backend.Version(context.Background(), "active")
	if err != nil || version.GitVersion != "v1.32.0" || gateway.profileID != "active" {
		t.Fatalf("version=%#v error=%v profile=%q", version, err, gateway.profileID)
	}
}

func TestRemoteBackendRequiresExactSessionBeforeDisconnect(t *testing.T) {
	sessions := &fakeSessions{current: clientremote.Session{ID: "session-a", Namespace: "default", State: "active"}}
	backend, err := NewRemoteBackend(RemoteDependencies{
		Profiles: fakeProfiles{state: clientprofile.State{
			ActiveProfileID: "active", Profiles: []clientprofile.Profile{{ID: "active"}},
		}},
		ControlPlane: &fakeControlPlane{},
		Sessions:     sessions,
		DataPlanes:   fakeDataPlanes{},
		ExecClient:   fakeExecClient{},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = backend.Disconnect(context.Background(), "active", "session-b", "default")
	var toolError *ToolError
	if !errors.As(err, &toolError) || toolError.Code != ErrorConflict {
		t.Fatalf("error=%#v", err)
	}
	if sessions.disconnectCalls != 0 {
		t.Fatal("mismatched Session was disconnected")
	}
}

func TestRemoteBackendUsesActiveSessionForPodFileOperations(t *testing.T) {
	gateway := &fakeControlPlane{}
	sessions := &fakeSessions{current: clientremote.Session{ID: "session-a", Namespace: "default", State: "active"}}
	backend, err := NewRemoteBackend(RemoteDependencies{
		Profiles: fakeProfiles{state: clientprofile.State{
			ActiveProfileID: "active", Profiles: []clientprofile.Profile{{ID: "active"}},
		}},
		ControlPlane: gateway, Sessions: sessions, DataPlanes: fakeDataPlanes{}, ExecClient: fakeExecClient{},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := TrafficIdentity{ProfileID: "active", SessionID: "session-a", Namespace: "default"}
	listing, err := backend.ListPodFiles(context.Background(), identity, clientremote.PodFileSpec{
		Pod: "api-0", Container: "api", Path: "/tmp",
	})
	if err != nil || listing.SessionID != "session-a" || gateway.podFile.Path != "/tmp" {
		t.Fatalf("listing=%#v gateway=%#v error=%v", listing, gateway, err)
	}
	task, err := backend.CreatePodFileOperation(context.Background(), identity, "delete", clientremote.PodFileSpec{
		Pod: "api-0", Container: "api", Path: "/tmp/old", Recursive: true,
	}, "delete-old")
	if err != nil || task.ID != "file-op-1" || gateway.podFileKey != "delete-old" || !gateway.podFile.Recursive {
		t.Fatalf("task=%#v gateway=%#v error=%v", task, gateway, err)
	}
}

func TestCappedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := newCappedBuffer(4)
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 || string(buffer.Bytes()) != "abcd" || !buffer.truncated {
		t.Fatalf("written=%d error=%v value=%q truncated=%t", written, err, buffer.Bytes(), buffer.truncated)
	}
}
