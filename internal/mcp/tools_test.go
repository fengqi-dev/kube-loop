package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/clientv2/exchange"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/clientv2/filetransfer"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/clientv2/portforward"
	clientremote "github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
)

type fakeBackend struct {
	session            clientremote.Session
	connectedProfile   string
	connectedNamespace string
	disconnectIdentity TrafficIdentity
	trafficRequest     TrafficStartRequest
	trafficStop        TrafficIdentity
	commandRequest     PodCommandRequest
	transferIdentity   TrafficIdentity
	transferRequest    clientfiletransfer.Request
	cancelIdentity     TrafficIdentity
}

func (backend *fakeBackend) Version(context.Context, string) (clientremote.Version, error) {
	return clientremote.Version{GitVersion: "v1.32.0"}, nil
}

func (backend *fakeBackend) Capabilities(_ context.Context, _ string, namespace string) (clientremote.Capabilities, error) {
	return clientremote.Capabilities{Namespace: namespace, GatewayVersion: "v2", Capabilities: []string{"pods.list"}}, nil
}

func (backend *fakeBackend) Namespaces(context.Context, string) ([]clientremote.Namespace, error) {
	return []clientremote.Namespace{{Name: "default"}, {Name: "team-a"}}, nil
}

func (backend *fakeBackend) Pods(_ context.Context, _ string, namespace string) ([]clientremote.Pod, error) {
	return []clientremote.Pod{{Name: "api-0", Namespace: namespace, Ready: true}}, nil
}

func (backend *fakeBackend) Services(_ context.Context, _ string, namespace string) ([]clientremote.Service, error) {
	return []clientremote.Service{{Name: "api", Namespace: namespace}}, nil
}

func (backend *fakeBackend) CurrentSession(string) (clientremote.Session, error) {
	if backend.session.ID == "" {
		return clientremote.Session{}, errors.New("not connected")
	}
	return backend.session, nil
}

func (backend *fakeBackend) Connect(_ context.Context, profileID, namespace string) (clientremote.Session, error) {
	backend.connectedProfile, backend.connectedNamespace = profileID, namespace
	backend.session = clientremote.Session{
		ID: "session-1", Namespace: namespace, State: "active", Generation: 1,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	return backend.session, nil
}

func (backend *fakeBackend) Disconnect(_ context.Context, profileID, sessionID, namespace string) error {
	backend.disconnectIdentity = TrafficIdentity{ProfileID: profileID, SessionID: sessionID, Namespace: namespace}
	return nil
}

func (backend *fakeBackend) StartTraffic(_ context.Context, request TrafficStartRequest) (TrafficItem, error) {
	backend.trafficRequest = request
	if request.Type == "port_forward" {
		info := clientportforward.Info{
			ID: "pf-1", ProfileID: request.ProfileID, SessionID: request.SessionID,
			Namespace: request.Namespace, Kind: request.TargetKind, Name: request.TargetName,
		}
		return TrafficItem{Type: request.Type, PortForward: &info}, nil
	}
	info := clientexchange.Info{
		ID: "exchange-1", ProfileID: request.ProfileID, SessionID: request.SessionID,
		Namespace: request.Namespace, Service: request.Service,
	}
	return TrafficItem{Type: request.Type, Exchange: &info}, nil
}

func (backend *fakeBackend) StopTraffic(_ context.Context, identity TrafficIdentity) error {
	backend.trafficStop = identity
	return nil
}

func (backend *fakeBackend) ListTraffic(profileID, trafficType string) ([]TrafficItem, error) {
	info := clientexchange.Info{ID: "exchange-1", ProfileID: profileID, SessionID: "session-1", Namespace: "default"}
	return []TrafficItem{{Type: trafficType, Exchange: &info}}, nil
}

func (backend *fakeBackend) ExecPodCommand(_ context.Context, request PodCommandRequest) (PodCommandResult, error) {
	backend.commandRequest = request
	return PodCommandResult{
		ProfileID: request.ProfileID, SessionID: request.SessionID, Namespace: request.Namespace,
		TaskID: "exec-1", Pod: request.Pod, Container: request.Container,
		Command: append([]string(nil), request.Command...), Stdout: []byte("hello\n"),
	}, nil
}

func (backend *fakeBackend) StartFileTransfer(identity TrafficIdentity, request clientfiletransfer.Request) (clientfiletransfer.Task, error) {
	backend.transferIdentity, backend.transferRequest = identity, request
	return clientfiletransfer.Task{
		ID: "transfer-1", ProfileID: identity.ProfileID, SessionID: identity.SessionID,
		Namespace: identity.Namespace, Direction: request.Direction, Kind: request.Kind,
		Pod: request.Pod, LocalPath: request.LocalPath, RemotePath: request.RemotePath,
	}, nil
}

func (backend *fakeBackend) ListFileTransfers(profileID string) ([]clientfiletransfer.Task, error) {
	return []clientfiletransfer.Task{{ID: "transfer-1", ProfileID: profileID, SessionID: "session-1", Namespace: "default"}}, nil
}

func (backend *fakeBackend) CancelFileTransfer(identity TrafficIdentity) error {
	backend.cancelIdentity = identity
	return nil
}

func TestManageClusterUsesExplicitProfile(t *testing.T) {
	backend := &fakeBackend{}
	output, err := manageCluster(context.Background(), backend, manageClusterIn{
		Action: "list", Type: "pod", ProfileID: "server-a", Namespace: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Pods) != 1 || output.Pods[0].Name != "api-0" || output.ProfileID != "server-a" {
		t.Fatalf("output=%#v", output)
	}
	_, err = manageCluster(context.Background(), backend, manageClusterIn{Action: "list", Type: "pod", ProfileID: "server-a"})
	assertToolError(t, err, ErrorInvalidArgument, "namespace")
}

func TestManageConnectionMutationCarriesExactIdentity(t *testing.T) {
	backend := &fakeBackend{}
	connected, err := manageConnection(context.Background(), backend, manageConnectionIn{
		Action: "connect", ProfileID: "server-a", Namespace: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if connected.Session.ID != "session-1" || backend.connectedProfile != "server-a" {
		t.Fatalf("connected=%#v backend=%#v", connected, backend)
	}
	_, err = manageConnection(context.Background(), backend, manageConnectionIn{
		Action: "disconnect", ProfileID: "server-a", SessionID: "session-1", Namespace: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend.disconnectIdentity.SessionID != "session-1" || backend.disconnectIdentity.Namespace != "default" {
		t.Fatalf("disconnect=%#v", backend.disconnectIdentity)
	}
}

func TestSensitiveToolsRejectImplicitSession(t *testing.T) {
	backend := &fakeBackend{}
	_, err := manageTraffic(context.Background(), backend, manageTrafficIn{
		Action: "start", Type: "port_forward", ProfileID: "server-a", Namespace: "default",
		TargetKind: "pod", TargetName: "api-0", RemotePort: 8080,
	})
	assertToolError(t, err, ErrorInvalidArgument, "sessionId")

	_, err = execPodCommand(context.Background(), backend, podCommandIn{
		ProfileID: "server-a", Namespace: "default", Pod: "api-0", Command: []string{"true"},
	})
	assertToolError(t, err, ErrorInvalidArgument, "sessionId")

	_, err = manageFileTransfer(backend, manageFileTransferIn{
		Action: "cancel", ProfileID: "server-a", Namespace: "default", TaskID: "transfer-1",
	})
	assertToolError(t, err, ErrorInvalidArgument, "sessionId")
}

func TestExecUsesExactArgvWithoutShellExpansion(t *testing.T) {
	backend := &fakeBackend{}
	result, err := execPodCommand(context.Background(), backend, podCommandIn{
		ProfileID: "server-a", SessionID: "session-1", Namespace: "default", Pod: "api-0",
		Container: "api", Command: []string{"printf", "%s", "$(id)"}, TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.commandRequest.Command) != 3 || backend.commandRequest.Command[2] != "$(id)" || result.TaskID != "exec-1" {
		t.Fatalf("request=%#v result=%#v", backend.commandRequest, result)
	}
}

func assertToolError(t *testing.T, err error, code ErrorCode, field string) {
	t.Helper()
	var toolError *ToolError
	if !errors.As(err, &toolError) || toolError.Code != code || toolError.Field != field {
		t.Fatalf("error=%#v want code=%q field=%q", err, code, field)
	}
}
