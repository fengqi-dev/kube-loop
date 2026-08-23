package exec

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

type blockingStreamClient struct {
	delegate    streamClient
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func (client *blockingStreamClient) CreateExecTask(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.ExecSpec,
	idempotencyKey string,
) (remote.ExecTask, error) {
	return client.delegate.CreateExecTask(ctx, serverProfile, session, spec, idempotencyKey)
}

func (client *blockingStreamClient) OpenExecStream(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	task remote.ExecTask,
) (*websocket.Conn, error) {
	client.startedOnce.Do(func() { close(client.started) })
	<-client.release
	return client.delegate.OpenExecStream(ctx, serverProfile, session, task)
}

func (client *blockingStreamClient) unblock() {
	client.releaseOnce.Do(func() { close(client.release) })
}

func TestManagerRoutesOutputInputResizeAndExitByProfileAndTask(t *testing.T) {
	input := make(chan execstream.Frame, 2)
	clientClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		for range 2 {
			_, encoded, readErr := connection.Read(request.Context())
			if readErr != nil {
				close(clientClosed)
				t.Error(readErr)
				return
			}
			frame, decodeErr := execstream.Decode(encoded)
			if decodeErr != nil {
				t.Error(decodeErr)
				return
			}
			input <- frame
		}
		output, _ := execstream.Encode(execstream.Frame{Type: execstream.Stdout, Payload: []byte("ready\r\n")})
		exit, _ := execstream.EncodeExit(execstream.ExitStatus{Code: 7, Error: "command exited unsuccessfully"})
		_ = connection.Write(request.Context(), websocket.MessageBinary, output)
		_ = connection.Write(request.Context(), websocket.MessageBinary, exit)
	}))
	defer server.Close()

	events := make(chan Event, 2)
	manager, err := NewManager(
		streamClient{endpoint: "ws" + strings.TrimPrefix(server.URL, "http")},
		ManagerConfig{OnEvent: func(event Event) { events <- event }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "server"}
	startContext, cancelStart := context.WithCancel(context.Background())
	task, err := manager.Start(
		startContext,
		serverProfile,
		remote.Session{ID: "session", Namespace: "development", State: "active"},
		remote.ExecSpec{Pod: "api-0", Command: []string{"/bin/sh"}, TTY: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelStart()
	select {
	case <-clientClosed:
		t.Fatal("Pod exec stream inherited the completed Start context")
	case <-time.After(100 * time.Millisecond):
	}
	if err := manager.Write(context.Background(), "other", task.ID, []byte("id\r")); err == nil {
		t.Fatal("cross-profile write was accepted")
	}
	if err := manager.Write(context.Background(), serverProfile.ID, task.ID, []byte("id\r")); err != nil {
		t.Fatal(err)
	}
	if err := manager.Resize(context.Background(), serverProfile.ID, task.ID, 120, 40); err != nil {
		t.Fatal(err)
	}
	firstInput, secondInput := <-input, <-input
	if firstInput.Type != execstream.Stdin || string(firstInput.Payload) != "id\r" ||
		secondInput.Type != execstream.Resize {
		t.Fatalf("input frames = %#v %#v", firstInput, secondInput)
	}
	firstEvent, secondEvent := receiveEvent(t, events), receiveEvent(t, events)
	decoded, decodeErr := base64.StdEncoding.DecodeString(firstEvent.Data)
	if decodeErr != nil || firstEvent.Type != EventStdout || string(decoded) != "ready\r\n" {
		t.Fatalf("output event = %#v decodeErr = %v", firstEvent, decodeErr)
	}
	if secondEvent.Type != EventExit || secondEvent.ExitCode != 7 || secondEvent.Error == "" {
		t.Fatalf("exit event = %#v", secondEvent)
	}
	if err := manager.Write(context.Background(), serverProfile.ID, task.ID, []byte("late")); err == nil {
		t.Fatal("completed stream remained active")
	}
}

func TestManagerStopProfileWaitsForOpeningStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		for {
			if _, _, err := connection.Read(request.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client := &blockingStreamClient{
		delegate: streamClient{endpoint: "ws" + strings.TrimPrefix(server.URL, "http")},
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	t.Cleanup(client.unblock)
	manager, err := NewManager(client, ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	profile := profile.Profile{ID: "server"}
	started := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(
			t.Context(),
			profile,
			remote.Session{ID: "session", Namespace: "development", State: "active"},
			remote.ExecSpec{Pod: "api-0", Command: []string{"/bin/sh"}},
		)
		started <- startErr
	}()
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("Pod exec Start did not reach stream opening")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.StopProfile(profile.ID) }()
	select {
	case err := <-stopped:
		t.Fatalf("StopProfile bypassed an in-flight Pod exec Start: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	client.unblock()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil && !strings.Contains(err.Error(), "closed network connection") {
		t.Fatal(err)
	}
	manager.mu.Lock()
	active := len(manager.active)
	manager.mu.Unlock()
	if active != 0 {
		t.Fatalf("Pod exec streams committed after StopProfile: %d", active)
	}
}

func TestManagerShutdownWaitsForEventCallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer checkTestClose(t, connection.CloseNow)
		output, _ := execstream.Encode(
			execstream.Frame{Type: execstream.Stdout, Payload: []byte("ready")},
		)
		_ = connection.Write(request.Context(), websocket.MessageBinary, output)
		for {
			if _, _, err := connection.Read(request.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	var callbackOnce sync.Once
	manager, err := NewManager(
		streamClient{endpoint: "ws" + strings.TrimPrefix(server.URL, "http")},
		ManagerConfig{OnEvent: func(Event) {
			callbackOnce.Do(func() { close(callbackStarted) })
			<-releaseCallback
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCallback) }) }
	t.Cleanup(release)
	if _, err := manager.Start(
		t.Context(),
		profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: "active"},
		remote.ExecSpec{Pod: "api-0", Command: []string{"/bin/sh"}},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-callbackStarted:
	case <-time.After(time.Second):
		t.Fatal("Pod exec event callback did not start")
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- manager.Shutdown() }()
	select {
	case err := <-shutdown:
		t.Fatalf("Shutdown returned before event callback completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-shutdown; err != nil && !strings.Contains(err.Error(), "closed network connection") {
		t.Fatal(err)
	}
}

func receiveEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Pod exec event")
		return Event{}
	}
}
