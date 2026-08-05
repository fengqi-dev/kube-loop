package cluster

import (
	"context"
	"fmt"
	"net/url"

	"github.com/fengqi-dev/kube-loop/internal/podssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var newRemoteExecutor = func(config *rest.Config, method string, target *url.URL) (remotecommand.Executor, error) {
	return remotecommand.NewSPDYExecutor(config, method, target)
}

// Exec streams a command through the Kubernetes pods/exec subresource.
func (p *Provider) Exec(
	ctx context.Context,
	target podssh.Target,
	command []string,
	streams podssh.Streams,
) error {
	if target.Context == "" || target.Namespace == "" || target.Pod == "" {
		return fmt.Errorf("context, namespace, and pod are required")
	}
	if len(command) == 0 {
		return fmt.Errorf("exec command is required")
	}
	config, err := p.RESTConfig(target.Context)
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}
	request := client.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Namespace(target.Namespace).
		Name(target.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: target.Container,
			Command:   command,
			Stdin:     streams.Stdin != nil,
			Stdout:    streams.Stdout != nil,
			Stderr:    streams.Stderr != nil && !streams.TTY,
			TTY:       streams.TTY,
		}, scheme.ParameterCodec)
	executor, err := newRemoteExecutor(config, "POST", request.URL())
	if err != nil {
		return fmt.Errorf("create pod exec stream: %w", err)
	}
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             streams.Stdin,
		Stdout:            streams.Stdout,
		Stderr:            streams.Stderr,
		Tty:               streams.TTY,
		TerminalSizeQueue: streams.TerminalSizeQueue,
	}); err != nil {
		return fmt.Errorf("exec %s/%s: %w", target.Namespace, target.Pod, err)
	}
	return nil
}
