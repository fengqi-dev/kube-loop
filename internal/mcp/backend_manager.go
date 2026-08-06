package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

const (
	defaultPodCommandTimeout = 30 * time.Second
	maxPodCommandTimeout     = 5 * time.Minute
	maxPodCommandOutput      = 1 << 20
)

type clusterControl interface {
	Inventory() (cluster.ClusterInventory, error)
	Probe(context.Context, string) cluster.ProbeResult
}

type sessionControl interface {
	State() session.State
	SetKubernetesVersion(string)
	Namespaces(context.Context, string) ([]string, error)
	ListServices(context.Context, string, string) ([]cluster.ServiceInfo, error)
	ListPods(context.Context, string, string) ([]cluster.PodInfo, error)
	RememberSelection(string, string) error
	Connect(context.Context, session.Request) error
	Disconnect() error
	ManualNetwork(string) cluster.ManualNetwork
	SetManualNetwork(string, cluster.ManualNetwork) error
	HostAliases(string) []store.HostAliasSpec
	SetHostAliases(string, []store.HostAliasSpec) error
	StartIntercept(context.Context, intercept.Mapping) (intercept.Info, error)
	StartMirror(context.Context, intercept.Mapping) (intercept.Info, error)
	StopIntercept(context.Context, string) error
	ListIntercepts() []intercept.Info
	ListMirrors() []intercept.Info
	StartPreview(context.Context, intercept.PreviewRequest) (intercept.Info, error)
	StopPreview(context.Context, string) error
	ListPreviews() []intercept.Info
	StartPortForwardSession(context.Context, portfwd.Request) (portfwd.Info, error)
	StopPortForward(string) error
	ListPortForwards() []portfwd.Info
	SingBoxConfig() ([]byte, error)
}

type fileTransferControl interface {
	Start(context.Context, filemanager.TransferRequest) (filemanager.TransferTask, error)
	ListTransfers() []filemanager.TransferTask
	Cancel(string) error
}

// managerBackend implements Backend against narrow application and cluster
// contracts. The MCP transport itself only depends on Backend.
type managerBackend struct {
	provider clusterControl
	manager  sessionControl
	executor podssh.Executor
	files    fileTransferControl
}

var _ Backend = managerBackend{}

func (b managerBackend) SessionState() session.State { return b.manager.State() }

func (b managerBackend) ReloadContexts() (cluster.ClusterInventory, error) {
	return b.provider.Inventory()
}

func (b managerBackend) ProbeContext(ctx context.Context, contextName string) (cluster.ProbeResult, error) {
	if contextName == "" {
		return cluster.ProbeResult{}, errors.New("context is required")
	}
	probeCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), 3*time.Second)
	defer cancel()
	result := b.provider.Probe(probeCtx, contextName)
	if result.OK && result.Version != "" {
		b.manager.SetKubernetesVersion(result.Version)
	}
	if result.OK {
		b.appendLog("INFO", fmt.Sprintf(
			"MCP cluster probe succeeded: context=%s version=%s latencyMs=%d",
			contextName, result.Version, result.LatencyMs,
		))
	} else {
		b.appendLog("WARN", fmt.Sprintf(
			"MCP cluster probe failed: context=%s latencyMs=%d error=%s",
			contextName, result.LatencyMs, result.Error,
		))
	}
	return result, nil
}

func (b managerBackend) Namespaces(ctx context.Context, contextName string) ([]string, error) {
	if contextName == "" {
		return nil, errors.New("context is required")
	}
	return b.manager.Namespaces(ctxOrBackground(ctx), contextName)
}

func (b managerBackend) ListServices(ctx context.Context, contextName, namespace string) ([]cluster.ServiceInfo, error) {
	if contextName == "" {
		return nil, errors.New("context is required")
	}
	return b.manager.ListServices(ctxOrBackground(ctx), contextName, namespace)
}

func (b managerBackend) ListPods(ctx context.Context, contextName, namespace string) ([]cluster.PodInfo, error) {
	if contextName == "" {
		return nil, errors.New("context is required")
	}
	return b.manager.ListPods(ctxOrBackground(ctx), contextName, namespace)
}

func (b managerBackend) Connect(ctx context.Context, contextName, namespace string) error {
	_ = b.manager.RememberSelection(contextName, namespace)
	return b.manager.Connect(ctxOrBackground(ctx), session.Request{
		Context:   contextName,
		Namespace: namespace,
	})
}

func (b managerBackend) Disconnect() error { return b.manager.Disconnect() }

func (b managerBackend) GetManualNetwork(contextName string) cluster.ManualNetwork {
	return b.manager.ManualNetwork(contextName)
}

func (b managerBackend) SetManualNetwork(contextName string, network cluster.ManualNetwork) error {
	return b.manager.SetManualNetwork(contextName, network)
}

func (b managerBackend) GetHostAliases(contextName string) []store.HostAliasSpec {
	return b.manager.HostAliases(contextName)
}

func (b managerBackend) SetHostAliases(contextName string, items []store.HostAliasSpec) error {
	return b.manager.SetHostAliases(contextName, items)
}

func (b managerBackend) StartIntercept(ctx context.Context, mapping intercept.Mapping) (intercept.Info, error) {
	return b.manager.StartIntercept(ctxOrBackground(ctx), mapping)
}

func (b managerBackend) StartMirror(ctx context.Context, mapping intercept.Mapping) (intercept.Info, error) {
	return b.manager.StartMirror(ctxOrBackground(ctx), mapping)
}

func (b managerBackend) StopIntercept(ctx context.Context, id string) error {
	return b.manager.StopIntercept(ctxOrBackground(ctx), id)
}

func (b managerBackend) ListIntercepts() []intercept.Info { return b.manager.ListIntercepts() }

func (b managerBackend) ListMirrors() []intercept.Info { return b.manager.ListMirrors() }

func (b managerBackend) StartPreview(ctx context.Context, request intercept.PreviewRequest) (intercept.Info, error) {
	return b.manager.StartPreview(ctxOrBackground(ctx), request)
}

func (b managerBackend) StopPreview(ctx context.Context, id string) error {
	return b.manager.StopPreview(ctxOrBackground(ctx), id)
}

func (b managerBackend) ListPreviews() []intercept.Info { return b.manager.ListPreviews() }

func (b managerBackend) StartPortForward(ctx context.Context, request portfwd.Request) (portfwd.Info, error) {
	return b.manager.StartPortForwardSession(ctxOrBackground(ctx), request)
}

func (b managerBackend) StopPortForward(id string) error { return b.manager.StopPortForward(id) }

func (b managerBackend) ListPortForwards() []portfwd.Info { return b.manager.ListPortForwards() }

func (b managerBackend) HelperStatus(ctx context.Context) helper.Status {
	return helper.GetStatus(ctxOrBackground(ctx))
}

func (b managerBackend) InstallHelper(ctx context.Context) error {
	b.appendLog("INFO", "MCP requested privileged helper installation")
	if err := helperinstall.EnsureInstall(ctxOrBackground(ctx)); err != nil {
		b.appendLog("ERROR", fmt.Sprintf("MCP install privileged helper: %v", err))
		return err
	}
	b.appendLog("INFO", "MCP privileged helper installation complete")
	return nil
}

func (b managerBackend) UninstallHelper(ctx context.Context) error {
	b.appendLog("INFO", "MCP requested privileged helper uninstall")
	if err := helperinstall.Uninstall(ctxOrBackground(ctx)); err != nil {
		b.appendLog("ERROR", fmt.Sprintf("MCP uninstall privileged helper: %v", err))
		return err
	}
	b.appendLog("INFO", "MCP privileged helper uninstalled")
	return nil
}

func (b managerBackend) SingBoxConfig() ([]byte, error) {
	return b.manager.SingBoxConfig()
}

func (b managerBackend) ExecPodCommand(
	ctx context.Context,
	request PodCommandRequest,
) (PodCommandResult, error) {
	if b.executor == nil {
		return PodCommandResult{}, errors.New("Pod command execution is unavailable")
	}
	if request.Context == "" || request.Namespace == "" || request.Pod == "" {
		return PodCommandResult{}, errors.New("context, namespace, and pod are required")
	}
	if strings.TrimSpace(request.Command) == "" {
		return PodCommandResult{}, errors.New("command is required")
	}
	pods, err := b.manager.ListPods(ctxOrBackground(ctx), request.Context, request.Namespace)
	if err != nil {
		return PodCommandResult{}, fmt.Errorf("verify Pod target: %w", err)
	}
	var selected *cluster.PodInfo
	for i := range pods {
		if pods[i].Namespace == request.Namespace && pods[i].Name == request.Pod {
			selected = &pods[i]
			break
		}
	}
	if selected == nil {
		return PodCommandResult{}, fmt.Errorf("Pod %s/%s not found", request.Namespace, request.Pod)
	}
	if request.PodUID != "" && selected.UID != request.PodUID {
		return PodCommandResult{}, errors.New("Pod was replaced")
	}
	if !selected.Ready {
		return PodCommandResult{}, errors.New("Pod is not ready")
	}
	container := request.Container
	if container == "" && len(selected.Containers) > 0 {
		container = selected.Containers[0]
	}
	if container == "" {
		return PodCommandResult{}, fmt.Errorf("Pod %s/%s has no regular containers", request.Namespace, request.Pod)
	}
	foundContainer := slices.Contains(selected.Containers, container)
	if !foundContainer {
		return PodCommandResult{}, fmt.Errorf(
			"container %q not found in Pod %s/%s",
			container, request.Namespace, request.Pod,
		)
	}

	timeout := defaultPodCommandTimeout
	if request.TimeoutSeconds < 0 {
		return PodCommandResult{}, errors.New("timeoutSeconds must not be negative")
	}
	if request.TimeoutSeconds > int(maxPodCommandTimeout/time.Second) {
		return PodCommandResult{}, fmt.Errorf(
			"timeoutSeconds must not exceed %d",
			int(maxPodCommandTimeout/time.Second),
		)
	}
	if request.TimeoutSeconds > 0 {
		timeout = time.Duration(request.TimeoutSeconds) * time.Second
	}
	commandCtx, cancel := context.WithTimeout(ctxOrBackground(ctx), timeout)
	defer cancel()
	stdout := newCappedBuffer(maxPodCommandOutput)
	stderr := newCappedBuffer(maxPodCommandOutput)
	execErr := b.executor.Exec(commandCtx, podssh.Target{
		Context: request.Context, Namespace: request.Namespace,
		Pod: request.Pod, Container: container,
	}, []string{"/bin/sh", "-c", request.Command}, podssh.Streams{
		Stdout: stdout,
		Stderr: stderr,
	})
	result := PodCommandResult{
		Context: request.Context, Namespace: request.Namespace,
		Pod: request.Pod, Container: container, Command: request.Command,
		Stdout: stdout.String(), Stderr: stderr.String(),
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
	}
	if execErr != nil {
		result.ExitCode = 1
		result.Error = execErr.Error()
		if exitErr, ok := errors.AsType[interface {
			error
			ExitStatus() int
		}](execErr); ok {
			result.ExitCode = exitErr.ExitStatus()
		} else if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			result.ExitCode = 124
		}
	}
	return result, nil
}

func (b managerBackend) StartFileTransfer(
	ctx context.Context,
	request filemanager.TransferRequest,
) (filemanager.TransferTask, error) {
	if b.files == nil {
		return filemanager.TransferTask{}, errors.New("file transfer is unavailable")
	}
	return b.files.Start(ctxOrBackground(ctx), request)
}

func (b managerBackend) ListFileTransfers() []filemanager.TransferTask {
	if b.files == nil {
		return nil
	}
	return b.files.ListTransfers()
}

func (b managerBackend) CancelFileTransfer(id string) error {
	if b.files == nil {
		return errors.New("file transfer is unavailable")
	}
	return b.files.Cancel(id)
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type cappedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	available := b.limit - b.Len()
	if available > 0 {
		write := min(len(value), available)
		_, _ = b.Buffer.Write(value[:write])
	}
	if len(value) > available {
		b.truncated = true
	}
	return len(value), nil
}

func (b managerBackend) appendLog(level, message string) {
	if logger, ok := b.manager.(interface{ AppendLog(string, string) }); ok {
		logger.AppendLog(level, message)
	}
}
