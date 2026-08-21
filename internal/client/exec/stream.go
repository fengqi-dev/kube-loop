package exec

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

type Client interface {
	CreateExecTask(context.Context, profile.Profile, remote.Session, remote.ExecSpec, string) (remote.ExecTask, error)
	OpenExecStream(context.Context, profile.Profile, remote.Session, remote.ExecTask) (*websocket.Conn, error)
}

type Stream struct {
	connection *websocket.Conn
	task       remote.ExecTask
	writeMu    sync.Mutex
	closeOnce  sync.Once
}

func Start(
	ctx context.Context,
	client Client,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.ExecSpec,
) (*Stream, error) {
	if client == nil {
		return nil, errors.New("pod exec client is required")
	}
	task, err := client.CreateExecTask(ctx, serverProfile, session, spec, "pod-exec:"+uuid.NewString())
	if err != nil {
		return nil, err
	}
	connection, err := client.OpenExecStream(ctx, serverProfile, session, task)
	if err != nil {
		return nil, err
	}
	return &Stream{connection: connection, task: task}, nil
}

func (stream *Stream) Task() remote.ExecTask { return stream.task }

func (stream *Stream) Read(ctx context.Context) (execstream.Frame, error) {
	messageType, encoded, err := stream.connection.Read(ctx)
	if err != nil {
		return execstream.Frame{}, err
	}
	if messageType != websocket.MessageBinary {
		return execstream.Frame{}, errors.New("pod exec stream returned a non-binary message")
	}
	frame, err := execstream.Decode(encoded)
	if err != nil {
		return execstream.Frame{}, err
	}
	if frame.Type != execstream.Stdout && frame.Type != execstream.Stderr && frame.Type != execstream.Exit {
		return execstream.Frame{}, errors.New("gateway sent a client-only Pod exec frame")
	}
	return frame, nil
}

func (stream *Stream) WriteStdin(ctx context.Context, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	return stream.write(ctx, execstream.Frame{Type: execstream.Stdin, Payload: payload})
}

func (stream *Stream) Resize(ctx context.Context, width, height uint16) error {
	encoded, err := execstream.EncodeResize(execstream.TerminalSize{Width: width, Height: height})
	if err != nil {
		return err
	}
	return stream.writeEncoded(ctx, encoded)
}

func (stream *Stream) CloseStdin(ctx context.Context) error {
	return stream.write(ctx, execstream.Frame{Type: execstream.CloseStdin})
}

func (stream *Stream) Close() error {
	var result error
	stream.closeOnce.Do(func() {
		result = stream.connection.Close(websocket.StatusNormalClosure, "client closed exec stream")
	})
	return result
}

func (stream *Stream) write(ctx context.Context, frame execstream.Frame) error {
	encoded, err := execstream.Encode(frame)
	if err != nil {
		return err
	}
	return stream.writeEncoded(ctx, encoded)
}

func (stream *Stream) writeEncoded(ctx context.Context, encoded []byte) error {
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	return stream.connection.Write(ctx, websocket.MessageBinary, encoded)
}
