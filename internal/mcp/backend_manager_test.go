package mcp

import (
	"context"
	"io"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
)

type podCommandSession struct {
	sessionControl
	pods []cluster.PodInfo
}

func (s podCommandSession) ListPods(context.Context, string, string) ([]cluster.PodInfo, error) {
	return s.pods, nil
}

type podCommandExecutor struct {
	target  podssh.Target
	command []string
	err     error
}

func (e *podCommandExecutor) Exec(
	_ context.Context,
	target podssh.Target,
	command []string,
	streams podssh.Streams,
) error {
	e.target = target
	e.command = append([]string{}, command...)
	_, _ = io.WriteString(streams.Stdout, "hello\n")
	_, _ = io.WriteString(streams.Stderr, "warning\n")
	return e.err
}

type commandExitError struct {
	code int
}

func (e commandExitError) Error() string   { return "command failed" }
func (e commandExitError) ExitStatus() int { return e.code }

func TestManagerBackendExecPodCommand(t *testing.T) {
	executor := &podCommandExecutor{err: commandExitError{code: 7}}
	backend := managerBackend{
		manager: podCommandSession{pods: []cluster.PodInfo{{
			Name: "api-0", Namespace: "default", UID: "uid-1", Ready: true,
			Containers: []string{"api", "sidecar"},
		}}},
		executor: executor,
	}
	result, err := backend.ExecPodCommand(context.Background(), PodCommandRequest{
		Context: "minikube", Namespace: "default", Pod: "api-0", PodUID: "uid-1",
		Command: "printf hello; exit 7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Container != "api" || result.Stdout != "hello\n" ||
		result.Stderr != "warning\n" || result.ExitCode != 7 ||
		result.Error != "command failed" {
		t.Fatalf("result=%#v", result)
	}
	if executor.target.Context != "minikube" || executor.target.Pod != "api-0" ||
		executor.target.Container != "api" {
		t.Fatalf("target=%#v", executor.target)
	}
	if len(executor.command) != 3 || executor.command[0] != "/bin/sh" ||
		executor.command[1] != "-c" || executor.command[2] != "printf hello; exit 7" {
		t.Fatalf("command=%#v", executor.command)
	}
}

func TestManagerBackendExecPodCommandRejectsReplacedPod(t *testing.T) {
	backend := managerBackend{
		manager: podCommandSession{pods: []cluster.PodInfo{{
			Name: "api-0", Namespace: "default", UID: "uid-new", Ready: true,
			Containers: []string{"api"},
		}}},
		executor: &podCommandExecutor{},
	}
	if _, err := backend.ExecPodCommand(context.Background(), PodCommandRequest{
		Context: "minikube", Namespace: "default", Pod: "api-0",
		PodUID: "uid-old", Command: "true",
	}); err == nil {
		t.Fatal("replaced Pod was accepted")
	}
}

func TestCappedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := newCappedBuffer(4)
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("written=%d err=%v", written, err)
	}
	if buffer.String() != "abcd" || !buffer.truncated {
		t.Fatalf("buffer=%q truncated=%t", buffer.String(), buffer.truncated)
	}
}
