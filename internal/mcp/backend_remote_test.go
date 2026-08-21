package mcp

import (
	"context"
	"errors"
	"testing"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

type fakeProfiles struct{ state clientprofile.State }

func (profiles fakeProfiles) Snapshot() clientprofile.State { return profiles.state }

type fakeControlPlane struct {
	versionCalls int
	profileID    string
	podFile      clientremote.PodFileSpec
	podFileKey   string
}

func (gateway *fakeControlPlane) Version(
	_ context.Context,
	profile clientprofile.Profile,
) (clientremote.Version, error) {
	gateway.versionCalls++
	gateway.profileID = profile.ID
	return clientremote.Version{GitVersion: "v1.32.0"}, nil
}

func (*fakeControlPlane) Capabilities(
	_ context.Context,
	_ clientprofile.Profile,
	namespace string,
) (clientremote.Capabilities, error) {
	return clientremote.Capabilities{Namespace: namespace}, nil
}
func (*fakeControlPlane) Namespaces(context.Context, clientprofile.Profile) ([]clientremote.Namespace, error) {
	return []clientremote.Namespace{{Name: "default"}}, nil
}
func (*fakeControlPlane) Pods(context.Context, clientprofile.Profile, string) ([]clientremote.Pod, error) {
	return nil, nil
}
func (*fakeControlPlane) Services(context.Context, clientprofile.Profile, string) ([]clientremote.Service, error) {
	return nil, nil
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

type fakeDataPlanes struct{}

func (fakeDataPlanes) Connect(
	context.Context,
	clientprofile.Profile,
	clientremote.Session,
) (clientdataplane.Status, error) {
	return clientdataplane.Status{}, nil
}
func (fakeDataPlanes) Disconnect(string) error { return nil }

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
