package execapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type Streams struct {
	Stdin             io.Reader
	Stdout            io.Writer
	Stderr            io.Writer
	TTY               bool
	TerminalSizeQueue remotecommand.TerminalSizeQueue
}

type Executor interface {
	Validate(context.Context, controlplaneapi.Principal, string, Spec) error
	Exec(context.Context, controlplaneapi.Principal, string, Spec, Streams) error
}

type Provider interface {
	ClientFor(authorization.Subject) (kubernetes.Interface, error)
	RESTConfigFor(authorization.Subject) (*rest.Config, error)
}

type KubernetesExecutor struct{ provider Provider }

func NewKubernetesExecutor(provider Provider) (*KubernetesExecutor, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes Provider is required")
	}
	return &KubernetesExecutor{provider: provider}, nil
}

func (executor *KubernetesExecutor) Validate(
	ctx context.Context,
	principal controlplaneapi.Principal,
	namespace string,
	spec Spec,
) error {
	subject := authorization.Subject{ID: principal.Subject, Groups: append([]string(nil), principal.Groups...)}
	client, err := executor.provider.ClientFor(subject)
	if err != nil {
		return err
	}
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, spec.Pod, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return fmt.Errorf("Pod %s/%s is not running", namespace, spec.Pod)
	}
	containers := make([]string, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers)+len(pod.Spec.EphemeralContainers))
	for _, container := range pod.Spec.InitContainers {
		containers = append(containers, container.Name)
	}
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
	}
	for _, container := range pod.Spec.EphemeralContainers {
		containers = append(containers, container.Name)
	}
	if spec.Container == "" {
		if len(containers) != 1 {
			return errors.New("container is required when the Pod has multiple containers")
		}
		return nil
	}
	if !slices.Contains(containers, spec.Container) {
		return fmt.Errorf("container %q does not exist in Pod %s/%s", spec.Container, namespace, spec.Pod)
	}
	return nil
}

func (executor *KubernetesExecutor) Exec(
	ctx context.Context,
	principal controlplaneapi.Principal,
	namespace string,
	spec Spec,
	streams Streams,
) error {
	subject := authorization.Subject{ID: principal.Subject, Groups: append([]string(nil), principal.Groups...)}
	config, err := executor.provider.RESTConfigFor(subject)
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes exec client: %w", err)
	}
	request := client.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).
		Name(spec.Pod).SubResource("exec").VersionedParams(&corev1.PodExecOptions{
		Container: spec.Container, Command: spec.Command,
		Stdin: streams.Stdin != nil, Stdout: streams.Stdout != nil,
		Stderr: streams.Stderr != nil && !streams.TTY, TTY: streams.TTY,
	}, scheme.ParameterCodec)
	remote, err := remotecommand.NewSPDYExecutor(config, "POST", request.URL())
	if err != nil {
		return fmt.Errorf("create Kubernetes exec stream: %w", err)
	}
	return remote.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin: streams.Stdin, Stdout: streams.Stdout, Stderr: streams.Stderr,
		Tty: streams.TTY, TerminalSizeQueue: streams.TerminalSizeQueue,
	})
}

var _ Executor = (*KubernetesExecutor)(nil)
