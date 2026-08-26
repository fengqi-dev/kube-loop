package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/trafficstream"
)

// OpenTrafficStream opens one reverse-traffic Task as a logical stream on the
// Runtime's current /tunnel WebSocket pool.
func (runtime *Runtime) OpenTrafficStream(
	ctx context.Context,
	mode string,
	taskID string,
) (*trafficstream.FrameConn, error) {
	if ctx == nil {
		return nil, errors.New("traffic stream context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.transportMu.Lock()
	if runtime.ctx.Err() != nil || runtime.forwarder == nil || runtime.token == (tunnel.SessionToken{}) ||
		signalClosed(runtime.transportDone) {
		runtime.transportMu.Unlock()
		return nil, errors.New("data Plane transport is not connected")
	}
	forwarder := runtime.forwarder
	control := runtime.control
	token := runtime.token
	transportDone := runtime.transportDone
	runtime.transportMu.Unlock()

	connection, err := forwarder.OpenStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open Data Plane logical stream: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = connection.Close()
		}
	}()
	clearDeadline, err := bindConnectionContext(ctx, connection)
	if err != nil {
		return nil, err
	}
	defer clearDeadline()
	if err := tunnel.WriteTrafficOpen(
		connection,
		tunnel.TrafficOpenRequest{Mode: mode, TaskID: taskID},
		token,
	); err != nil {
		return nil, fmt.Errorf("open Traffic Task stream: %w", contextConnectionError(ctx, err))
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		return nil, fmt.Errorf("start Traffic Task stream: %w", contextConnectionError(ctx, err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.transportMu.Lock()
	runtimeActive := runtime.ctx.Err() == nil
	transportMatches := runtime.forwarder == forwarder && runtime.control == control && runtime.token == token
	current := runtimeActive && transportMatches && runtime.transportDone == transportDone &&
		!signalClosed(transportDone)
	if current {
		if runtime.streams == nil {
			runtime.streams = make(map[chan struct{}]*transportStreams)
		}
		streams := runtime.streams[transportDone]
		if streams == nil {
			streams = &transportStreams{forwarder: forwarder, control: control}
			runtime.streams[transportDone] = streams
		}
		streams.count++
		connection = &trackedTrafficConn{
			Conn: connection,
			release: func() {
				runtime.releaseTrafficStream(transportDone)
			},
		}
	}
	runtime.transportMu.Unlock()
	if !current {
		return nil, errors.New("data Plane transport changed while opening Traffic Task stream")
	}
	framed, err := trafficstream.Dial(ctx, connection)
	if err != nil {
		return nil, fmt.Errorf("upgrade Traffic Task stream to WebSocket: %w", err)
	}
	closeOnError = false
	return framed, nil
}

func (runtime *Runtime) releaseTrafficStream(transportDone chan struct{}) {
	var draining *transportStreams
	runtime.transportMu.Lock()
	streams := runtime.streams[transportDone]
	if streams != nil {
		if streams.count > 0 {
			streams.count--
		}
		if streams.count == 0 {
			delete(runtime.streams, transportDone)
			if streams.draining {
				draining = streams
			}
		}
	}
	runtime.transportMu.Unlock()
	if draining != nil {
		_ = closeConnection(draining.control)
		_ = closeForwarder(draining.forwarder)
	}
}

func bindConnectionContext(ctx context.Context, connection net.Conn) (func(), error) {
	deadline := time.Time{}
	if value, ok := ctx.Deadline(); ok {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set Traffic stream startup deadline: %w", err)
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = connection.SetDeadline(time.Now())
		close(finished)
	})
	return func() {
		if !stop() {
			<-finished
		}
		_ = connection.SetDeadline(time.Time{})
	}, nil
}

func contextConnectionError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func isAddressAlreadyInUse(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}
