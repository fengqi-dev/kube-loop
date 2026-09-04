package mirror

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/taskrelay"
)

type restoreMirrorClient struct {
	testMirrorClient

	tasks []remote.MirrorTask
}

func (client *restoreMirrorClient) ListMirrors(
	context.Context, profile.Profile, remote.Session,
) ([]remote.MirrorTask, error) {
	return append([]remote.MirrorTask(nil), client.tasks...), nil
}

func TestManagerRestoreReopensReleasedMirrorWithoutRemotePause(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: mirrorSessionActive}
	task := remote.MirrorTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []remote.MirrorPort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restoreMirrorClient{
		task:  task,
		tasks: []remote.MirrorTask{task},
	}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	manager.Track(profile.Profile{ID: "server"}, session, taskrelay.Task{
		ID: task.ID, SessionID: session.ID, Namespace: session.Namespace,
		Service: task.Service, ClusterIP: task.ClusterIP,
		Targets: []LocalTarget{{Protocol: "tcp", ServicePort: 80, LocalHost: "127.0.0.1", LocalPort: 8080}},
	})
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err == nil {
		t.Fatal("Restore() reopened a released Mirror without a local Traffic stream")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 0 {
		t.Fatalf("restore calls = open %d, stop %d; want open 1, stop 0", openCalls, stopCalls)
	}
	if items := manager.List("server"); len(items) != 1 || items[0].State != taskrelay.StatePaused {
		t.Fatalf("released Mirror was dropped or resumed: %#v", items)
	}
}

func TestManagerRestoreRehydratesStoppedMirror(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: mirrorSessionActive}
	task := remote.MirrorTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []remote.MirrorPort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restoreMirrorClient{tasks: []remote.MirrorTask{task}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].State != "paused" || items[0].Targets[0].LocalPort != 8080 {
		t.Fatalf("restored mirrors = %#v", items)
	}
}

func TestManagerRestoreFailureDoesNotPauseRunningMirror(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: mirrorSessionActive}
	task := remote.MirrorTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []remote.MirrorPort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restoreMirrorClient{
		task:  task,
		tasks: []remote.MirrorTask{task},
	}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err == nil {
		t.Fatal("Restore() succeeded without a local Traffic stream")
	}
	_, stopCalls := client.calls()
	if stopCalls != 0 {
		t.Fatalf("remote stop calls = %d", stopCalls)
	}
}
