package podssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"

	clientexec "github.com/fengqi-dev/kube-loop/internal/clientv2/exec"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
)

type SessionSource interface {
	Current(string) (remote.Session, error)
}

type Config struct {
	ServerOptions []localpodssh.Option
}

type Request struct {
	ProfileID  string   `json:"profileId"`
	Namespace  string   `json:"namespace"`
	Pod        string   `json:"pod"`
	Container  string   `json:"container,omitempty"`
	PodIP      string   `json:"podIp"`
	Ready      bool     `json:"ready"`
	Containers []string `json:"containers"`
}

type Info struct {
	ID         string   `json:"id"`
	ProfileID  string   `json:"profileId"`
	SessionID  string   `json:"sessionId"`
	Namespace  string   `json:"namespace"`
	Pod        string   `json:"pod"`
	Container  string   `json:"container"`
	Containers []string `json:"containers"`
	PodIP      string   `json:"podIp"`
	Address    string   `json:"address"`
	Port       uint16   `json:"port"`
	Command    string   `json:"command"`
	State      string   `json:"state"`
}

type activeEndpoint struct {
	profile  profile.Profile
	session  remote.Session
	target   localpodssh.Target
	listener net.Listener
	info     Info
	wait     sync.WaitGroup
}

type Manager struct {
	client   clientexec.Client
	sessions SessionSource
	server   *localpodssh.Server

	mu       sync.Mutex
	active   map[string]*activeEndpoint
	starting map[string]struct{}
}

func New(client clientexec.Client, sessions SessionSource, config Config) (*Manager, error) {
	if client == nil || sessions == nil {
		return nil, errors.New("Pod SSH remote exec client and Session source are required")
	}
	manager := &Manager{
		client: client, sessions: sessions,
		active: make(map[string]*activeEndpoint), starting: make(map[string]struct{}),
	}
	manager.server = localpodssh.NewServer(remoteExecutor{manager: manager}, config.ServerOptions...)
	return manager, nil
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	if ctx == nil {
		return Info{}, errors.New("Pod SSH context is required")
	}
	if strings.TrimSpace(request.ProfileID) != serverProfile.ID || session.State != "active" {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.Pod = strings.TrimSpace(request.Pod)
	request.PodIP = strings.TrimSpace(request.PodIP)
	request.Container = strings.TrimSpace(request.Container)
	if request.Namespace == "" || request.Pod == "" || request.PodIP == "" || session.Namespace != request.Namespace {
		return Info{}, errors.New("Pod SSH target must belong to the active Session namespace")
	}
	if !request.Ready {
		return Info{}, errors.New("Pod SSH target must be ready")
	}
	containers := normalizeContainers(request.Containers)
	if len(containers) == 0 {
		return Info{}, errors.New("Pod SSH target has no containers")
	}
	if request.Container == "" {
		request.Container = containers[0]
	}
	if !slices.Contains(containers, request.Container) {
		return Info{}, fmt.Errorf("container %q is not available in Pod %s/%s", request.Container, request.Namespace, request.Pod)
	}
	reservation := serverProfile.ID + "\x00" + request.Namespace + "\x00" + request.Pod
	manager.mu.Lock()
	if _, exists := manager.starting[reservation]; exists || manager.findLocked(serverProfile.ID, request.Namespace, request.Pod) != nil {
		manager.mu.Unlock()
		return Info{}, errors.New("Pod SSH endpoint is already active")
	}
	manager.starting[reservation] = struct{}{}
	manager.mu.Unlock()
	committed := false
	defer func() {
		manager.mu.Lock()
		delete(manager.starting, reservation)
		manager.mu.Unlock()
		if !committed {
			// Enable may not have reached the server; Disable is intentionally best effort.
			_ = manager.server.Disable(serverProfile.ID + "/" + request.Namespace + "/" + request.Pod)
		}
	}()

	target := localpodssh.Target{
		Context: serverProfile.ID, Namespace: request.Namespace, Pod: request.Pod,
		Container: request.Container, Containers: containers, IP: request.PodIP,
	}
	baseInfo, err := manager.server.Enable(target)
	if err != nil {
		return Info{}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Info{}, fmt.Errorf("listen for Pod SSH: %w", err)
	}
	localInfo, err := manager.server.EndpointInfo(baseInfo.ID, listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return Info{}, err
	}
	entry := &activeEndpoint{
		profile: serverProfile, session: session, target: target, listener: listener,
		info: Info{
			ID: localInfo.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
			Namespace: request.Namespace, Pod: request.Pod, Container: request.Container,
			Containers: containers, PodIP: request.PodIP, Address: listener.Addr().String(),
			Port: localInfo.Port, Command: localInfo.Command, State: "active",
		},
	}
	manager.mu.Lock()
	delete(manager.starting, reservation)
	manager.active[entry.info.ID] = entry
	manager.mu.Unlock()
	committed = true
	entry.wait.Add(1)
	go manager.accept(entry)
	return entry.info, nil
}

func (manager *Manager) Stop(profileID, endpointID string) error {
	manager.mu.Lock()
	entry := manager.active[endpointID]
	if entry != nil && entry.profile.ID == profileID {
		delete(manager.active, endpointID)
	} else {
		entry = nil
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("Pod SSH endpoint is not active")
	}
	listenerErr := entry.listener.Close()
	serverErr := manager.server.Disable(endpointID)
	entry.wait.Wait()
	return errors.Join(normalizeCloseError(listenerErr), serverErr)
}

func (manager *Manager) StopProfile(profileID string) error {
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID {
			ids = append(ids, id)
		}
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(profileID, id))
	}
	return result
}

func (manager *Manager) List(profileID string) []Info {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]Info, 0, len(manager.active))
	for _, entry := range manager.active {
		if profileID == "" || entry.profile.ID == profileID {
			item := entry.info
			item.Containers = append([]string(nil), item.Containers...)
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(left, right Info) int { return strings.Compare(left.ID, right.ID) })
	return items
}

func (manager *Manager) Shutdown() error {
	manager.mu.Lock()
	ids := make([]string, 0, len(manager.active))
	for id := range manager.active {
		ids = append(ids, id)
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		manager.mu.Lock()
		entry := manager.active[id]
		manager.mu.Unlock()
		if entry != nil {
			result = errors.Join(result, manager.Stop(entry.profile.ID, id))
		}
	}
	return result
}

func (manager *Manager) accept(entry *activeEndpoint) {
	defer entry.wait.Done()
	for {
		connection, err := entry.listener.Accept()
		if err != nil {
			return
		}
		entry.wait.Go(func() {
			_ = manager.server.ServeConnection(entry.info.ID, connection)
		})
	}
}

func (manager *Manager) lookup(target localpodssh.Target) (profile.Profile, remote.Session, error) {
	manager.mu.Lock()
	entry := manager.findLocked(target.Context, target.Namespace, target.Pod)
	if entry == nil || !slices.Contains(entry.target.Containers, target.Container) {
		manager.mu.Unlock()
		return profile.Profile{}, remote.Session{}, errors.New("Pod SSH endpoint is no longer active")
	}
	serverProfile := entry.profile
	expectedSession := entry.session
	manager.mu.Unlock()

	current, err := manager.sessions.Current(serverProfile.ID)
	if err != nil {
		return profile.Profile{}, remote.Session{}, err
	}
	if current.ID != expectedSession.ID || current.Namespace != target.Namespace || current.State != "active" {
		return profile.Profile{}, remote.Session{}, errors.New("Pod SSH Session changed")
	}
	return serverProfile, current, nil
}

func (manager *Manager) findLocked(profileID, namespace, pod string) *activeEndpoint {
	for _, entry := range manager.active {
		if entry.profile.ID == profileID && entry.info.Namespace == namespace && entry.info.Pod == pod {
			return entry
		}
	}
	return nil
}

func normalizeContainers(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeCloseError(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type remoteExecutor struct {
	manager *Manager
}

func (executor remoteExecutor) Exec(
	ctx context.Context,
	target localpodssh.Target,
	command []string,
	streams localpodssh.Streams,
) error {
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
	defer stream.Close()
	if streams.Stdin != nil {
		go pumpInput(execContext, cancel, stream, streams.Stdin)
	}
	if streams.TTY && streams.TerminalSizeQueue != nil {
		go pumpTerminalSizes(execContext, cancel, stream, streams.TerminalSizeQueue)
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
