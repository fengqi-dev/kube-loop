package websocket

import (
	"context"
	"fmt"
	"time"
)

type deadlineStop struct {
	stop  func() bool
	fired chan struct{}
}

func prepareContextDeadline(ctx context.Context, setDeadline func(time.Time) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	return setDeadline(deadline)
}

func interruptOnCancel(ctx context.Context, setDeadline func(time.Time) error) deadlineStop {
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(fired)
	})
	return deadlineStop{stop: stop, fired: fired}
}

func finishContextDeadline(stop deadlineStop, setDeadline func(time.Time) error) {
	if !stop.stop() {
		<-stop.fired
	}
	_ = setDeadline(time.Time{})
}

func operationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return fmt.Errorf("WebSocket operation: %w", contextErr)
	}
	return err
}

// NetConn exposes binary or text WebSocket messages as a byte stream. Each
