package exec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

type streamClient struct{ endpoint string }

type blockingCloseConnection struct {
	started chan struct{}
	release chan struct{}
	err     error
	calls   atomic.Int32
}

func (*blockingCloseConnection) Read(context.Context) (websocket.MessageType, []byte, error) {
	return 0, nil, errors.New("unexpected read")
}

func (*blockingCloseConnection) Write(context.Context, websocket.MessageType, []byte) error {
	return errors.New("unexpected write")
}

func (connection *blockingCloseConnection) Close(websocket.StatusCode, string) error {
	connection.calls.Add(1)
	close(connection.started)
	<-connection.release
	return connection.err
}

func (client streamClient) CreateExecTask(
	context.Context, profile.Profile, remote.Session, remote.ExecSpec, string,
) (remote.ExecTask, error) {
	now := time.Now().UTC()
	return remote.ExecTask{
		ID:        uuid.NewString(),
		SessionID: "session",
		Namespace: "development",
		State:     "pending",
		Pod:       "api-0",
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(time.Minute),
	}, nil
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
		defer checkTestClose(t, connection.CloseNow)
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
	stream, err := Start(
		context.Background(),
		streamClient{endpoint: "ws" + strings.TrimPrefix(server.URL, "http")},
		profile.Profile{ID: "server"},
		remote.Session{ID: "session", Namespace: "development", State: "active"},
		remote.ExecSpec{Pod: "api-0", Command: []string{"/bin/sh"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, stream.Close)
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

func TestStreamConcurrentCloseRetainsError(t *testing.T) {
	closeFailure := errors.New("close exec stream")
	connection := &blockingCloseConnection{
		started: make(chan struct{}), release: make(chan struct{}), err: closeFailure,
	}
	stream := &Stream{connection: connection}
	results := make(chan error, 2)
	go func() { results <- stream.Close() }()
	go func() { results <- stream.Close() }()
	<-connection.started
	close(connection.release)
	for range 2 {
		if err := <-results; !errors.Is(err, closeFailure) {
			t.Fatalf("Close() error = %v, want %v", err, closeFailure)
		}
	}
	if calls := connection.calls.Load(); calls != 1 {
		t.Fatalf("connection close calls = %d, want 1", calls)
	}
}
