package preview

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type restorePreviewClient struct {
	testPreviewClient

	tasks []remote.PreviewTask
}

func (client *restorePreviewClient) ListPreviews(
	context.Context, profile.Profile, remote.Session,
) ([]remote.PreviewTask, error) {
	return append([]remote.PreviewTask(nil), client.tasks...), nil
}

func TestManagerRestoreReopensReleasedPreviewWithoutRemotePause(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: previewSessionActive}
	task := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Name: "local-api", ClusterIP: "10.96.0.42",
		Ports:        []remote.PreviewPort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now,
	}
	client := &restorePreviewClient{
		created: task,
		tasks:   []remote.PreviewTask{task},
	}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.active[task.ID] = &activePreview{
		profile: profile.Profile{ID: "server"}, session: session, task: task,
		info: Info{
			ID: task.ID, ProfileID: "server", SessionID: session.ID, Namespace: session.Namespace,
			Name: task.Name, ClusterIP: task.ClusterIP, State: previewStatePaused,
			Targets: []LocalTarget{{Protocol: "tcp", ServicePort: 80, LocalHost: "127.0.0.1", LocalPort: 8080}},
		},
	}
	manager.mu.Unlock()
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err == nil {
		t.Fatal("Restore() reopened a released Preview without a local Traffic stream")
	}
	_, openCalls, _, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 0 {
		t.Fatalf("restore calls = open %d, stop %d; want open 1, stop 0", openCalls, stopCalls)
	}
	if items := manager.List("server"); len(items) != 1 || items[0].State != previewStatePaused {
		t.Fatalf("released Preview was dropped or resumed: %#v", items)
	}
}

func TestManagerRestoreRehydratesStoppedPreview(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: previewSessionActive}
	task := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Name: "local-api", ClusterIP: "10.96.0.42",
		Ports:        []remote.PreviewPort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now,
	}
	client := &restorePreviewClient{tasks: []remote.PreviewTask{task}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].State != "paused" || items[0].Targets[0].LocalPort != 8080 {
		t.Fatalf("restored previews = %#v", items)
	}
}

func TestManagerRestoreFailureDoesNotPauseRunningPreview(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: previewSessionActive}
	task := remote.PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Name: "local-api", ClusterIP: "10.96.0.42",
		Ports:        []remote.PreviewPort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now,
	}
	client := &restorePreviewClient{
		created: task,
		running: task,
		tasks:   []remote.PreviewTask{task},
	}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err == nil {
		t.Fatal("Restore() succeeded without a local Traffic stream")
	}
	_, _, _, stopCalls := client.calls()
	if stopCalls != 0 {
		t.Fatalf("remote stop calls = %d", stopCalls)
	}
}
