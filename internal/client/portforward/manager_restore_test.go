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

func TestManagerRestoreStartsRunningPortForwardLocallyWithoutRemoteResume(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Kind: "service", Name: "api", Protocol: "tcp",
		RemotePort: 8443, LocalPort: 18443, DialAddress: "10.96.0.20:8443",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	client.task = task
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	if err := manager.Restore(t.Context(), profile.Profile{ID: "server"}, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].State != "active" || len(locals.started) != 1 {
		t.Fatalf("reconciled Port Forwards = %#v, local starts = %#v", items, locals.started)
	}
	client.mu.Lock()
	resumeCalls := len(client.resumed)
	client.mu.Unlock()
	if resumeCalls != 0 {
		t.Fatalf("remote Resume calls = %d", resumeCalls)
	}
}

func TestManagerRestoreStopsLocalPortForwardWhenCRDIsPaused(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Kind: "service", Name: "api", Protocol: "tcp",
		RemotePort: 8443, LocalPort: 18443, DialAddress: "10.96.0.20:8443",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	client.task = task
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server"}
	if err := manager.Restore(t.Context(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	task.State = "stopped"
	client.tasks = []remote.PortForwardTask{task}
	if err := manager.Restore(t.Context(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List("server")
	if len(items) != 1 || items[0].State != "paused" || len(locals.stopped) != 1 {
		t.Fatalf("reconciled Port Forwards = %#v, local stops = %#v", items, locals.stopped)
	}
	client.mu.Lock()
	pauseCalls := len(client.paused)
	client.mu.Unlock()
	if pauseCalls != 0 {
		t.Fatalf("remote Pause calls = %d", pauseCalls)
	}
}

func TestManagerReleaseProfileReleasesLocalThenRestoreReestablishes(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Kind: "service", Name: "api", Protocol: "tcp",
		RemotePort: 8443, LocalPort: 18443, DialAddress: "10.96.0.20:8443",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	client.task = task
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server"}
	//nolint:staticcheck // This test intentionally verifies defensive rejection of a nil context.
	if err := manager.ReleaseProfile(nil, serverProfile.ID); err == nil {
		t.Fatal("nil ReleaseProfile context was accepted")
	}
	if _, err := manager.Start(t.Context(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: task.Kind, Name: task.Name,
		Protocol: task.Protocol, RemotePort: task.RemotePort,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReleaseProfile(t.Context(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	items := manager.List(serverProfile.ID)
	if len(items) != 1 || items[0].State != "paused" {
		t.Fatalf("released Port Forwards = %#v", items)
	}
	if len(locals.stopped) != 1 || locals.stopped[0] != "local-1" {
		t.Fatalf("local stops = %#v", locals.stopped)
	}
	client.mu.Lock()
	pauseCalls, stopCalls := len(client.paused), len(client.stopped)
	client.mu.Unlock()
	if pauseCalls != 0 || stopCalls != 0 {
		t.Fatalf("ReleaseProfile touched the Gateway: pause=%v stop=%v", client.paused, client.stopped)
	}
	if err := manager.ReleaseProfile(t.Context(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if len(locals.stopped) != 1 {
		t.Fatalf("repeated ReleaseProfile was not idempotent: local stops = %#v", locals.stopped)
	}
	if err := manager.Restore(t.Context(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	items = manager.List(serverProfile.ID)
	if len(items) != 1 || items[0].State != "active" || len(locals.started) != 2 ||
		len(locals.stopped) != 1 {
		t.Fatalf(
			"restored Port Forwards = %#v, local starts = %#v, local stops = %#v",
			items, locals.started, locals.stopped,
		)
	}
	client.mu.Lock()
	resumeCalls := len(client.resumed)
	client.mu.Unlock()
	if resumeCalls != 0 {
		t.Fatalf("Restore issued remote Resume calls = %d", resumeCalls)
	}
}

func TestManagerRestoreReestablishesReleasedAutoLocalPort(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	// The Gateway does not persist a desktop-side allocated local port, so the
	// listed task carries LocalPort 0. The released entry must still re-bind to
	// the local port it knows about (previously auto-allocated by the desktop).
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Kind: "service", Name: "api", Protocol: "tcp",
		RemotePort: 8443, LocalPort: 0, DialAddress: "10.96.0.20:8443",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	client.task = task
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server"}
	if _, err := manager.Start(t.Context(), serverProfile, session, Request{
		ProfileID: serverProfile.ID, Kind: task.Kind, Name: task.Name,
		Protocol: task.Protocol, RemotePort: task.RemotePort,
	}); err != nil {
		t.Fatal(err)
	}
	if items := manager.List(serverProfile.ID); len(items) != 1 || items[0].LocalPort != 49152 {
		t.Fatalf("started Port Forwards = %#v", items)
	}
	if err := manager.ReleaseProfile(t.Context(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(t.Context(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List(serverProfile.ID)
	if len(items) != 1 || items[0].State != "active" || items[0].LocalPort != 49152 ||
		len(locals.started) != 2 || len(locals.stopped) != 1 {
		t.Fatalf(
			"restored Port Forwards = %#v, local starts = %#v, local stops = %#v",
			items, locals.started, locals.stopped,
		)
	}
	client.mu.Lock()
	resumeCalls := len(client.resumed)
	client.mu.Unlock()
	if resumeCalls != 0 {
		t.Fatalf("Restore issued remote Resume calls = %d", resumeCalls)
	}
}

func TestManagerRestoreKeepsAutoLocalPortPausedThenResumeRebinds(t *testing.T) {
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: portForwardSessionActive}
	// Simulate a client restart: the TaskClient only knows the CRD state, whose
	// LocalPort is 0 because the Gateway never persists desktop-side allocations.
	// Restore must keep a usable (paused) entry so a later Resume can re-bind.
	task := remote.PortForwardTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "running", Kind: "service", Name: "api", Protocol: "tcp",
		RemotePort: 8443, LocalPort: 0, DialAddress: "10.96.0.20:8443",
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	client := &restorePortForwardClient{tasks: []remote.PortForwardTask{task}}
	client.task = task
	manager, err := New(client, fakeDataPlane{})
	if err != nil {
		t.Fatal(err)
	}
	locals := &fakeLocals{}
	manager.locals = locals
	serverProfile := profile.Profile{ID: "server"}
	if err := manager.Restore(t.Context(), serverProfile, session); err != nil {
		t.Fatal(err)
	}
	items := manager.List(serverProfile.ID)
	if len(items) != 1 || items[0].State != "paused" || items[0].LocalPort != 0 || len(locals.started) != 0 {
		t.Fatalf("restored Port Forwards = %#v, local starts = %#v", items, locals.started)
	}
	if _, err := manager.Resume(t.Context(), serverProfile.ID, task.ID); err != nil {
		t.Fatalf("resume after restart failed: %v", err)
	}
	items = manager.List(serverProfile.ID)
	if len(items) != 1 || items[0].State != "active" || items[0].LocalPort != 49152 ||
		len(locals.started) != 1 {
		t.Fatalf("resumed Port Forwards = %#v, local starts = %#v", items, locals.started)
	}
	client.mu.Lock()
	resumeCalls := len(client.resumed)
	client.mu.Unlock()
	if resumeCalls != 1 {
		t.Fatalf("remote Resume calls = %d", resumeCalls)
	}
	if len(locals.started) != 1 || locals.started[0].LocalPort != 0 {
		t.Fatalf("Resume did not re-allocate the local port: %#v", locals.started)
	}
}
