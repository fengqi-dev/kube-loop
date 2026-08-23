package portforward

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/portforward/listener"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/traffic"
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
	started  []listener.Request
	stopped  []string
}

type blockingLocals struct {
	*fakeLocals

	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func (locals *blockingLocals) StartResolved(
	ctx context.Context,
	request listener.Request,
	dialAddress string,
	dialer listener.TrafficDialer,
) (listener.Info, error) {
	locals.startedOnce.Do(func() { close(locals.started) })
	<-locals.release
	return locals.fakeLocals.StartResolved(ctx, request, dialAddress, dialer)
}

func (locals *blockingLocals) unblock() {
	locals.releaseOnce.Do(func() { close(locals.release) })
}

func (locals *fakeLocals) StartResolved(
	_ context.Context,
	request listener.Request,
	_ string,
	_ listener.TrafficDialer,
) (listener.Info, error) {
	locals.started = append(locals.started, request)
	if locals.startErr != nil {
		return listener.Info{}, locals.startErr
	}
	return listener.Info{ID: "local-1", LocalPort: 49152, Address: "127.0.0.1:49152"}, nil
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
	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
	if err := manager.StopProfile(nil, "server-1"); err == nil {
		t.Fatal("nil StopProfile context was accepted")
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server-1", BaseURL: "https://gateway.example.test"}
	session := remote.Session{ID: task.SessionID, Namespace: task.Namespace, State: portForwardSessionActive}
	info, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != task.ID || info.Address != "127.0.0.1:49152" || len(locals.started) != 1 ||
		locals.started[0].Context != serverProfile.ID {
		t.Fatalf("info = %#v local requests = %#v", info, locals.started)
	}
	if items := manager.List(serverProfile.ID); len(items) != 1 || items[0].LocalPort != 49152 {
		t.Fatalf("active = %#v", items)
	}
	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
	if err := manager.Stop(nil, serverProfile.ID, task.ID); err == nil {
		t.Fatal("nil stop context was accepted")
	}
	if len(manager.List(serverProfile.ID)) != 1 || len(locals.stopped) != 0 || len(client.stopped) != 0 {
		t.Fatal("rejected stop mutated the active Port Forward")
	}
	if err := manager.Stop(context.Background(), serverProfile.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background(), serverProfile.ID, task.ID); err == nil {
		t.Fatal("repeated stop succeeded")
	}
	if len(locals.stopped) != 1 || locals.stopped[0] != "local-1" || len(client.stopped) != 1 ||
		client.stopped[0] != task.ID {
		t.Fatalf("local stops = %#v remote stops = %#v", locals.stopped, client.stopped)
	}
}

func TestManagerStopProfileWaitsForStartingForward(t *testing.T) {
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
	locals := &blockingLocals{
		fakeLocals: &fakeLocals{}, started: make(chan struct{}), release: make(chan struct{}),
	}
	t.Cleanup(locals.unblock)
	manager.locals = locals
	profile := profile.Profile{ID: "server-1"}
	session := remote.Session{ID: task.SessionID, Namespace: task.Namespace, State: portForwardSessionActive}
	started := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(t.Context(), profile, session, Request{
			ProfileID: profile.ID, Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
		})
		started <- startErr
	}()
	select {
	case <-locals.started:
	case <-time.After(time.Second):
		t.Fatal("Port Forward Start did not reach local listener creation")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.StopProfile(t.Context(), profile.ID) }()
	select {
	case err := <-stopped:
		t.Fatalf("StopProfile bypassed an in-flight Port Forward Start: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	locals.unblock()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if items := manager.List(profile.ID); len(items) != 0 {
		t.Fatalf("Port Forward committed after StopProfile: %#v", items)
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
		ID: task.SessionID, Namespace: task.Namespace, State: portForwardSessionActive,
	}, Request{ProfileID: "server-1", Kind: "pod", Name: "api-0", RemotePort: 8080, LocalPort: 8080})
	if err == nil || len(client.stopped) != 1 || client.stopped[0] != task.ID || len(manager.List("server-1")) != 0 {
		t.Fatalf("error = %v remote stops = %#v active = %#v", err, client.stopped, manager.List("server-1"))
	}
}

func TestManagerRejectsUnboundSessionBeforeCreatingForward(t *testing.T) {
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
	serverProfile := profile.Profile{ID: "server-1"}
	active := remote.Session{ID: task.SessionID, Namespace: task.Namespace, State: portForwardSessionActive}
	request := Request{ProfileID: serverProfile.ID, Kind: "service", Name: "api", RemotePort: 8443}
	tests := []struct {
		name    string
		ctx     context.Context
		session remote.Session
		request Request
	}{
		{name: "nil context", session: active, request: request},
		{
			name:    "inactive session",
			ctx:     context.Background(),
			session: remote.Session{State: "stopped"},
			request: request,
		},
		{
			name:    "wrong profile",
			ctx:     context.Background(),
			session: active,
			request: Request{ProfileID: "server-2", Kind: "service", Name: "api", RemotePort: 8443},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := manager.Start(test.ctx, serverProfile, test.session, test.request); err == nil {
				t.Fatal("unbound forward was accepted")
			}
		})
	}
	if len(locals.started) != 0 || len(client.stopped) != 0 || len(manager.List("")) != 0 {
		t.Fatalf("local starts=%#v remote stops=%#v active=%#v", locals.started, client.stopped, manager.List(""))
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
	session := remote.Session{ID: task.SessionID, Namespace: task.Namespace, State: portForwardSessionActive}
	if _, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: "api", Protocol: "tcp", RemotePort: 8443,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(manager.List("")) != 0 || len(locals.stopped) != 1 || len(client.stopped) != 1 ||
		client.stopped[0] != task.ID {
		t.Fatalf("active = %#v local stops = %#v remote stops = %#v", manager.List(""), locals.stopped, client.stopped)
	}
	if _, err := manager.Start(context.Background(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: "api", RemotePort: 8443,
	}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Shutdown error = %v, want ErrClosed", err)
	}
}

func TestManagerStopProfileDoesNotAffectAnotherServer(t *testing.T) {
	client := &fakeTaskClient{}
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	taskA := remote.PortForwardTask{ID: uuid.NewString()}
	taskB := remote.PortForwardTask{ID: uuid.NewString()}
	manager.active[taskA.ID] = &activeForward{
		profile: profile.Profile{ID: "server-a"}, session: remote.Session{ID: uuid.NewString()},
		task: taskA, localID: "local-a", info: Info{ID: taskA.ID, ProfileID: "server-a"},
	}
	manager.active[taskB.ID] = &activeForward{
		profile: profile.Profile{ID: "server-b"}, session: remote.Session{ID: uuid.NewString()},
		task: taskB, localID: "local-b", info: Info{ID: taskB.ID, ProfileID: "server-b"},
	}

	if err := manager.StopProfile(context.Background(), "server-a"); err != nil {
		t.Fatal(err)
	}
	if len(manager.List("server-a")) != 0 || len(manager.List("server-b")) != 1 {
		t.Fatalf("active forwards = %#v", manager.List(""))
	}
	if len(locals.stopped) != 1 || locals.stopped[0] != "local-a" ||
		len(client.stopped) != 1 || client.stopped[0] != taskA.ID {
		t.Fatalf("local stops=%#v remote stops=%#v", locals.stopped, client.stopped)
	}
	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(manager.List("")) != 0 || len(locals.stopped) != 2 || locals.stopped[1] != "local-b" {
		t.Fatalf("remaining active=%#v local stops=%#v", manager.List(""), locals.stopped)
	}
}
