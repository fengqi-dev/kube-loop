package portforward

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/traffic"
	"github.com/google/uuid"
)

type fakeTaskClient struct {
	mu        sync.Mutex
	task      remote.PortForwardTask
	stopped   []string
	createErr error
}

func (client *fakeTaskClient) CreatePortForward(
	context.Context,
	profile.Profile,
	remote.Session,
	remote.PortForwardSpec,
	string,
) (remote.PortForwardTask, error) {
	return client.task, client.createErr
}

func (client *fakeTaskClient) StopPortForward(
	_ context.Context,
	_ profile.Profile,
	_ remote.Session,
	taskID string,
) (remote.PortForwardTask, error) {
	client.mu.Lock()
	client.stopped = append(client.stopped, taskID)
	client.mu.Unlock()
	task := client.task
	task.State = "stopped"
	return task, nil
}

type fakeDataPlane struct{}

func (fakeDataPlane) Dialer(string) (traffic.Dialer, error) { return traffic.Dialer{}, nil }

type fakeLocals struct {
	startErr error
	started  []portfwd.Request
	stopped  []string
}

func (locals *fakeLocals) StartResolved(request portfwd.Request, _ string, _ portfwd.TrafficDialer) (portfwd.Info, error) {
	locals.started = append(locals.started, request)
	if locals.startErr != nil {
		return portfwd.Info{}, locals.startErr
	}
	return portfwd.Info{ID: "local-1", LocalPort: 49152, Address: "127.0.0.1:49152"}, nil
}

func (locals *fakeLocals) Stop(id string) error {
	locals.stopped = append(locals.stopped, id)
	return nil
}

func TestManagerBindsGatewayTaskToLocalOnlyListener(t *testing.T) {
	now := time.Now().UTC()
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: uuid.NewString(), Namespace: "development", State: "running",
		Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
		DialAddress: "10.96.0.20:8443", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	client := &fakeTaskClient{task: task}
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server-1", BaseURL: "https://gateway.example.test"}
	session := remote.Session{ID: task.SessionID, Namespace: task.Namespace, State: "active"}
	info, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != task.ID || info.Address != "127.0.0.1:49152" || len(locals.started) != 1 || locals.started[0].Context != serverProfile.ID {
		t.Fatalf("info = %#v local requests = %#v", info, locals.started)
	}
	if items := manager.List(serverProfile.ID); len(items) != 1 || items[0].LocalPort != 49152 {
		t.Fatalf("active = %#v", items)
	}
	if err := manager.Stop(context.Background(), serverProfile.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if len(locals.stopped) != 1 || locals.stopped[0] != "local-1" || len(client.stopped) != 1 || client.stopped[0] != task.ID {
		t.Fatalf("local stops = %#v remote stops = %#v", locals.stopped, client.stopped)
	}
}

func TestManagerRollsBackGatewayTaskWhenLocalPortIsOccupied(t *testing.T) {
	now := time.Now().UTC()
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: uuid.NewString(), Namespace: "development", State: "running",
		Kind: "pod", Name: "api-0", Protocol: "tcp", RemotePort: 8080,
		DialAddress: "10.244.1.7:8080", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	client := &fakeTaskClient{task: task}
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	manager.locals = &fakeLocals{startErr: errors.New("address already in use")}
	_, err = manager.Start(context.Background(), profile.Profile{ID: "server-1"}, remote.Session{
		ID: task.SessionID, Namespace: task.Namespace, State: "active",
	}, Request{ProfileID: "server-1", Kind: "pod", Name: "api-0", RemotePort: 8080, LocalPort: 8080})
	if err == nil || len(client.stopped) != 1 || client.stopped[0] != task.ID || len(manager.List("server-1")) != 0 {
		t.Fatalf("error = %v remote stops = %#v active = %#v", err, client.stopped, manager.List("server-1"))
	}
}

func TestManagerShutdownStopsLocalAndRemoteTasks(t *testing.T) {
	now := time.Now().UTC()
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: uuid.NewString(), Namespace: "development", State: "running",
		Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
		DialAddress: "10.96.0.20:8443", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	client := &fakeTaskClient{task: task}
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server-1", BaseURL: "https://gateway.example.test"}
	session := remote.Session{ID: task.SessionID, Namespace: task.Namespace, State: "active"}
	if _, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(manager.List("")) != 0 || len(locals.stopped) != 1 || len(client.stopped) != 1 || client.stopped[0] != task.ID {
		t.Fatalf("active = %#v local stops = %#v remote stops = %#v", manager.List(""), locals.stopped, client.stopped)
	}
}
