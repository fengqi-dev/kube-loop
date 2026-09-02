package exchange

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type restoreExchangeClient struct {
	testExchangeClient

	tasks []remote.ExchangeTask
}

func (client *restoreExchangeClient) ListExchanges(
	context.Context, profile.Profile, remote.Session,
) ([]remote.ExchangeTask, error) {
	return append([]remote.ExchangeTask(nil), client.tasks...), nil
}

func TestManagerRestoreRehydratesStoppedExchange(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: exchangeSessionActive}
	task := remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "stopped", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []remote.ExchangePort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restoreExchangeClient{tasks: []remote.ExchangeTask{task}}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].State != "paused" || items[0].Targets[0].LocalPort != 8080 {
		t.Fatalf("restored exchanges = %#v", items)
	}
}

func TestManagerRestoreFailureDoesNotPauseRunningExchange(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: exchangeSessionActive}
	task := remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []remote.ExchangePort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restoreExchangeClient{
		task:  task,
		tasks: []remote.ExchangeTask{task},
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

func TestManagerRestoreReopensReleasedExchangeWithoutRemotePause(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: exchangeSessionActive}
	task := remote.ExchangeTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Service: "api", ClusterIP: "10.96.0.20",
		Ports:        []remote.ExchangePort{{ServicePort: 80, Protocol: "tcp"}},
		LocalTargets: []remote.LocalTarget{{ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080}},
		CreatedAt:    now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restoreExchangeClient{
		task:  task,
		tasks: []remote.ExchangeTask{task},
	}
	manager, err := NewManager(client, Config{TrafficStreams: client})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.active[task.ID] = &activeExchange{
		profile: profile.Profile{ID: "server"}, session: session, task: task,
		info: Info{
			ID: task.ID, ProfileID: "server", SessionID: session.ID, Namespace: session.Namespace,
			Service: task.Service, ClusterIP: task.ClusterIP, State: exchangeStatePaused,
			Targets: []LocalTarget{{Protocol: "tcp", ServicePort: 80, LocalHost: "127.0.0.1", LocalPort: 8080}},
		},
	}
	manager.mu.Unlock()
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err == nil {
		t.Fatal("Restore() reopened a released Exchange without a local Traffic stream")
	}
	openCalls, stopCalls := client.calls()
	if openCalls != 1 || stopCalls != 0 {
		t.Fatalf("restore calls = open %d, stop %d; want open 1, stop 0", openCalls, stopCalls)
	}
	if items := manager.List("server"); len(items) != 1 || items[0].State != exchangeStatePaused {
		t.Fatalf("released Exchange was dropped or resumed: %#v", items)
	}
}
