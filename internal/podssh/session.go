package podssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/tools/remotecommand"
)

type sessionState struct {
	ctx       context.Context
	cancel    context.CancelFunc
	channel   ssh.Channel
	target    Target
	executor  Executor
	terminal  *terminalSizeQueue
	tty       bool
	startOnce sync.Once
}

func (s *Server) serveSession(
	channel ssh.Channel,
	requests <-chan *ssh.Request,
	target Target,
) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &sessionState{
		ctx: ctx, cancel: cancel, channel: channel, target: target, executor: s.executor,
		terminal: newTerminalSizeQueue(),
	}
	defer func() {
		cancel()
		state.terminal.Close()
		_ = channel.Close()
	}()
	for request := range requests {
		switch request.Type {
		case "pty-req":
			var payload ptyRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			state.tty = true
			state.terminal.Push(payload.Columns, payload.Rows)
			_ = request.Reply(true, nil)
		case "window-change":
			var payload windowChangeRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err == nil {
				state.terminal.Push(payload.Columns, payload.Rows)
			}
		case "env":
			// Kubernetes exec inherits the container environment. Accepting the
			// request keeps common SSH clients compatible without mutating it.
			_ = request.Reply(true, nil)
		case "shell":
			started := false
			state.startOnce.Do(func() {
				started = true
				_ = request.Reply(true, nil)
				go state.runExec([]string{"/bin/sh"})
			})
			if !started {
				_ = request.Reply(false, nil)
			}
		case "exec":
			var payload execRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			started := false
			state.startOnce.Do(func() {
				started = true
				_ = request.Reply(true, nil)
				go state.runExec([]string{"/bin/sh", "-c", payload.Command})
			})
			if !started {
				_ = request.Reply(false, nil)
			}
		case "subsystem":
			var payload subsystemRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Name != "sftp" {
				_ = request.Reply(false, nil)
				continue
			}
			started := false
			state.startOnce.Do(func() {
				started = true
				_ = request.Reply(true, nil)
				go state.runSFTP()
			})
			if !started {
				_ = request.Reply(false, nil)
			}
		case "signal":
			cancel()
			_ = request.Reply(true, nil)
		default:
			_ = request.Reply(false, nil)
		}
	}
}

func (s *sessionState) runExec(command []string) {
	streams := Streams{
		Stdin:  s.channel,
		Stdout: s.channel,
		Stderr: s.channel.Stderr(),
		TTY:    s.tty,
	}
	if s.tty {
		streams.Stderr = nil
		streams.TerminalSizeQueue = s.terminal
	}
	err := s.executor.Exec(s.ctx, s.target, command, streams)
	status := uint32(0)
	if err != nil {
		status = 1
		_, _ = fmt.Fprintln(s.channel.Stderr(), err)
	}
	_, _ = s.channel.SendRequest("exit-status", false, ssh.Marshal(exitStatus{Status: status}))
	_ = s.channel.Close()
	s.cancel()
}

func (s *sessionState) runSFTP() {
	handler := newSFTPHandler(s.executor, s.target)
	server := sftp.NewRequestServer(s.channel, sftp.Handlers{
		FileGet: handler, FilePut: handler, FileCmd: handler, FileList: handler,
	}, sftp.WithStartDirectory("/"))
	err := server.Serve()
	status := uint32(0)
	if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
		status = 1
	}
	_, _ = s.channel.SendRequest("exit-status", false, ssh.Marshal(exitStatus{Status: status}))
	_ = server.Close()
	_ = s.channel.Close()
	s.cancel()
}

type ptyRequest struct {
	Term          string
	Columns, Rows uint32
	Width, Height uint32
	TerminalModes string
}

type windowChangeRequest struct {
	Columns, Rows uint32
	Width, Height uint32
}

type execRequest struct {
	Command string
}

type subsystemRequest struct {
	Name string
}

type exitStatus struct {
	Status uint32
}

type terminalSizeQueue struct {
	sizes chan remotecommand.TerminalSize
	done  chan struct{}
	once  sync.Once
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{
		sizes: make(chan remotecommand.TerminalSize, 1),
		done:  make(chan struct{}),
	}
}

func (q *terminalSizeQueue) Push(width, height uint32) {
	size := remotecommand.TerminalSize{Width: uint16(width), Height: uint16(height)}
	select {
	case <-q.done:
		return
	default:
	}
	select {
	case q.sizes <- size:
	default:
		select {
		case <-q.sizes:
		default:
		}
		select {
		case q.sizes <- size:
		default:
		}
	}
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.sizes:
		return &size
	case <-q.done:
		return nil
	}
}

func (q *terminalSizeQueue) Close() {
	q.once.Do(func() { close(q.done) })
}
