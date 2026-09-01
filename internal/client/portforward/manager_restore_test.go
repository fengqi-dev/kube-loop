package portforward

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type restorePortForwardClient struct {
	fakeTaskClient

	tasks []remote.PortForwardTask
}

func (client *restorePortForwardClient) ListPortForwards(
	context.Context, profile.Profile, remote.Session,
) ([]remote.PortForwardTask, error) {
	return append([]remote.PortForwardTask(nil), client.tasks...), nil
}

func TestManagerRestoreRehydratesStoppedPortForward(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Kind: "service", Name: "api", Protocol: "tcp",
		RemotePort: 8443, LocalPort: 18443, DialAddress: "10.96.0.20:8443",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].State != "paused" || items[0].LocalPort != 18443 {
		t.Fatalf("restored Port Forwards = %#v", items)
	}
}

func TestManagerRestoreDoesNotRehydrateDeletedPortForward(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Kind: "pod", Name: "api-0", Protocol: "tcp",
		RemotePort: 8080, LocalPort: 18080, DialAddress: "10.2.0.10:8080",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	manager.deleted[task.ID] = struct{}{}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err != nil {
		t.Fatal(err)
	}
	if items := manager.List("server"); len(items) != 0 {
		t.Fatalf("deleted Port Forward was restored: %#v", items)
	}
}
