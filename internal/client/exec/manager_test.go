package exec

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

func TestManagerRoutesOutputInputResizeAndExitByProfileAndTask(t *testing.T) {
	input := make(chan execstream.Frame, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		for range 2 {
			_, encoded, readErr := connection.Read(request.Context())
			if readErr != nil {
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
	manager, err := NewManager(streamClient{endpoint: "ws" + strings.TrimPrefix(server.URL, "http")}, ManagerConfig{OnEvent: func(event Event) { events <- event }})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "server"}
	task, err := manager.Start(context.Background(), serverProfile, remote.Session{ID: "session", Namespace: "development", State: "active"}, remote.ExecSpec{Pod: "api-0", Command: []string{"/bin/sh"}, TTY: true})
	if err != nil {
		t.Fatal(err)
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
	if firstInput.Type != execstream.Stdin || string(firstInput.Payload) != "id\r" || secondInput.Type != execstream.Resize {
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
