package exec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/google/uuid"
)

type streamClient struct{ endpoint string }

func (client streamClient) CreateExecTask(
	context.Context, profile.Profile, remote.Session, remote.ExecSpec, string,
) (remote.ExecTask, error) {
	now := time.Now().UTC()
	return remote.ExecTask{ID: uuid.NewString(), SessionID: "session", Namespace: "development", State: "pending", Pod: "api-0", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute)}, nil
}

func (client streamClient) OpenExecStream(
	ctx context.Context, _ profile.Profile, _ remote.Session, _ remote.ExecTask,
) (*websocket.Conn, error) {
	connection, _, err := websocket.Dial(ctx, client.endpoint, nil)
	return connection, err
}

func TestTypedStreamSeparatesInputResizeOutputAndExit(t *testing.T) {
	received := make(chan execstream.Frame, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		for range 2 {
			_, encoded, err := connection.Read(request.Context())
			if err != nil {
				t.Error(err)
				return
			}
			frame, err := execstream.Decode(encoded)
			if err != nil {
				t.Error(err)
				return
			}
			received <- frame
		}
		stdout, _ := execstream.Encode(execstream.Frame{Type: execstream.Stdout, Payload: []byte("ready")})
		_ = connection.Write(request.Context(), websocket.MessageBinary, stdout)
	}))
	defer server.Close()
	stream, err := Start(context.Background(), streamClient{endpoint: "ws" + strings.TrimPrefix(server.URL, "http")}, profile.Profile{ID: "server"}, remote.Session{ID: "session", Namespace: "development", State: "active"}, remote.ExecSpec{Pod: "api-0", Command: []string{"/bin/sh"}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.WriteStdin(context.Background(), []byte("echo\n")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Resize(context.Background(), 120, 40); err != nil {
		t.Fatal(err)
	}
	first, second := <-received, <-received
	if first.Type != execstream.Stdin || string(first.Payload) != "echo\n" || second.Type != execstream.Resize {
		t.Fatalf("frames = %#v %#v", first, second)
	}
	frame, err := stream.Read(context.Background())
	if err != nil || frame.Type != execstream.Stdout || string(frame.Payload) != "ready" {
		t.Fatalf("frame = %#v err = %v", frame, err)
	}
}
