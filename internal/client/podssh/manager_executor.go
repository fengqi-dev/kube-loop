package podssh

import (
	"context"
	"errors"
	"fmt"
	"io"

	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
)

type remoteExecutor struct {
	manager *Manager
}

func (executor remoteExecutor) Exec(
	ctx context.Context,
	target localpodssh.Target,
	command []string,
	streams localpodssh.Streams,
) (resultErr error) {
	serverProfile, session, err := executor.manager.lookup(target)
	if err != nil {
		return err
	}
	execContext, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := clientexec.Start(execContext, executor.manager.client, serverProfile, session, remote.ExecSpec{
		Pod: target.Pod, Container: target.Container, Command: append([]string(nil), command...), TTY: streams.TTY,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := stream.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close Pod exec stream: %w", err))
		}
	}()
	if (streams.Stdin != nil || (streams.TTY && streams.TerminalSizeQueue != nil)) && streams.Go == nil {
		return errors.New("pod SSH stream worker owner is required")
	}
	if streams.Stdin != nil {
		streams.Go(func() { pumpInput(execContext, cancel, stream, streams.Stdin) })
	}
	if streams.TTY && streams.TerminalSizeQueue != nil {
		streams.Go(func() {
			pumpTerminalSizes(execContext, cancel, stream, streams.TerminalSizeQueue)
		})
	}
	for {
		frame, err := stream.Read(execContext)
		if err != nil {
			if execContext.Err() != nil {
				return execContext.Err()
			}
			return err
		}
		switch frame.Type {
		case execstream.Stdout:
			if streams.Stdout != nil {
				if _, err := streams.Stdout.Write(frame.Payload); err != nil {
					return err
				}
			}
		case execstream.Stderr:
			if streams.Stderr != nil {
				if _, err := streams.Stderr.Write(frame.Payload); err != nil {
					return err
				}
			}
		case execstream.Exit:
			status, err := execstream.DecodeExit(frame)
			if err != nil {
				return err
			}
			if status.Code != 0 || status.Cancelled {
				return fmt.Errorf("remote Pod exec exited with code %d: %s", status.Code, status.Error)
			}
			return nil
		}
	}
}

func pumpInput(ctx context.Context, cancel context.CancelFunc, stream *clientexec.Stream, input io.Reader) {
	buffer := make([]byte, 32<<10)
	for {
		count, err := input.Read(buffer)
		if count > 0 {
			if writeErr := stream.WriteStdin(ctx, buffer[:count]); writeErr != nil {
				cancel()
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = stream.CloseStdin(ctx)
			} else {
				cancel()
			}
			return
		}
	}
}

func pumpTerminalSizes(
	ctx context.Context,
	cancel context.CancelFunc,
	stream *clientexec.Stream,
	sizes localpodssh.TerminalSizeQueue,
) {
	for {
		size := sizes.Next()
		if size == nil {
			return
		}
		if err := stream.Resize(ctx, size.Width, size.Height); err != nil {
			cancel()
			return
		}
	}
}
