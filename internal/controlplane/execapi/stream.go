package execapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"k8s.io/client-go/tools/remotecommand"
)

type frameWriter struct {
	ctx        context.Context
	connection *websocket.Conn
	frameType  byte
	mu         *sync.Mutex
}

func (writer frameWriter) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	encoded, err := execstream.Encode(execstream.Frame{Type: writer.frameType, Payload: payload})
	if err != nil {
		return 0, err
	}
	writer.mu.Lock()
	err = writer.connection.Write(writer.ctx, websocket.MessageBinary, encoded)
	writer.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

type terminalSizeQueue struct {
	sizes chan remotecommand.TerminalSize
	done  chan struct{}
	once  sync.Once
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{sizes: make(chan remotecommand.TerminalSize, 1), done: make(chan struct{})}
}

func (queue *terminalSizeQueue) Push(size execstream.TerminalSize) {
	value := remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
	select {
	case <-queue.done:
		return
	default:
	}
	select {
	case queue.sizes <- value:
	default:
		select {
		case <-queue.sizes:
		default:
		}
		select {
		case queue.sizes <- value:
		default:
		}
	}
}

func (queue *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-queue.sizes:
		return &size
	case <-queue.done:
		return nil
	}
}

func (queue *terminalSizeQueue) Close() { queue.once.Do(func() { close(queue.done) }) }

func readInput(
	ctx context.Context,
	connection *websocket.Conn,
	stdin *io.PipeWriter,
	sizes *terminalSizeQueue,
) error {
	defer stdin.Close()
	for {
		messageType, encoded, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		if messageType != websocket.MessageBinary {
			return errors.New("exec stream accepts binary frames only")
		}
		frame, err := execstream.Decode(encoded)
		if err != nil {
			return err
		}
		switch frame.Type {
		case execstream.Stdin:
			if _, err := stdin.Write(frame.Payload); err != nil {
				return err
			}
		case execstream.Resize:
			size, err := execstream.DecodeResize(frame)
			if err != nil {
				return err
			}
			sizes.Push(size)
		case execstream.CloseStdin:
			return nil
		default:
			return errors.New("client sent a server-only exec frame")
		}
	}
}

func taskResult(status execstream.ExitStatus) json.RawMessage {
	result, _ := json.Marshal(struct {
		ExitCode  uint32 `json:"exitCode"`
		Cancelled bool   `json:"cancelled,omitempty"`
		Error     string `json:"error,omitempty"`
	}{ExitCode: status.Code, Cancelled: status.Cancelled, Error: status.Error})
	return result
}
