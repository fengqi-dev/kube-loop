package podssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if ctx == nil {
		return Info{}, errors.New("pod SSH context is required")
	}
	if strings.TrimSpace(request.ProfileID) != serverProfile.ID || session.State != podSSHSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.Pod = strings.TrimSpace(request.Pod)
	request.PodIP = strings.TrimSpace(request.PodIP)
	request.Container = strings.TrimSpace(request.Container)
	if request.Namespace == "" || request.Pod == "" || request.PodIP == "" || session.Namespace != request.Namespace {
		return Info{}, errors.New("pod SSH target must belong to the active Session namespace")
	}
	if !request.Ready {
		return Info{}, errors.New("pod SSH target must be ready")
	}
	containers := normalizeContainers(request.Containers)
	if len(containers) == 0 {
		return Info{}, errors.New("pod SSH target has no containers")
	}
	if request.Container == "" {
		request.Container = containers[0]
	}
	if !slices.Contains(containers, request.Container) {
		return Info{}, fmt.Errorf(
			"container %q is not available in Pod %s/%s",
			request.Container,
			request.Namespace,
			request.Pod,
		)
	}
	reservation := serverProfile.ID + "\x00" + request.Namespace + "\x00" + request.Pod
	manager.mu.Lock()
	if _, exists := manager.starting[reservation]; exists {
		manager.mu.Unlock()
		return Info{}, errors.New("pod SSH endpoint is already active")
	}
	if existing := manager.findLocked(serverProfile.ID, request.Namespace, request.Pod); existing != nil {
		info := existing.info
		manager.mu.Unlock()
		command, err := manager.server.Command(info.ID, request.Container)
		if err != nil {
			return Info{}, err
		}
		info.Container = request.Container
		info.Command = command
		return info, nil
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
	if manager.hostTCP == nil {
		return Info{}, errors.New("native PodIP SSH interception is unavailable")
	}
	if err := manager.hostTCP.SetHostTCPHandler(
		serverProfile.ID,
		func(host string, port uint16) (func(net.Conn), bool) {
			return manager.server.HostTCPForContext(serverProfile.ID, host, port)
		},
	); err != nil {
		return Info{}, err
	}
	entry := &activeEndpoint{
		profile: serverProfile, session: session, target: target,
		info: Info{
			ID: baseInfo.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
			Namespace: request.Namespace, Pod: request.Pod, Container: request.Container,
			Containers: containers, PodIP: request.PodIP, Address: net.JoinHostPort(request.PodIP, "22"),
			Port: baseInfo.Port, Command: baseInfo.Command, State: podSSHSessionActive,
		},
	}
	manager.mu.Lock()
	delete(manager.starting, reservation)
	manager.active[entry.info.ID] = entry
	manager.mu.Unlock()
	committed = true
	return entry.info, nil
}
