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
	request, containers, err := normalizeStartRequest(serverProfile, session, request)
	if err != nil {
		return Info{}, err
	}
	reservation, existing, err := manager.reserveStart(serverProfile.ID, request)
	if err != nil {
		return Info{}, err
	}
	if existing != nil {
		return manager.reuseEndpoint(existing, request.Container)
	}
	committed := false
	defer func() {
		manager.releaseStart(reservation, endpointID(serverProfile.ID, request), committed)
	}()

	target := localpodssh.Target{
		Context: serverProfile.ID, Namespace: request.Namespace, Pod: request.Pod,
		Container: request.Container, Containers: containers, IP: request.PodIP,
	}
	entry, err := manager.enableEndpoint(serverProfile, session, request, containers, target)
	if err != nil {
		return Info{}, err
	}
	manager.commitStart(reservation, entry)
	committed = true
	return entry.info, nil
}

func normalizeStartRequest(
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Request, []string, error) {
	if strings.TrimSpace(request.ProfileID) != serverProfile.ID || session.State != podSSHSessionActive {
		return Request{}, nil, errors.New("active Server Profile Session is required")
	}
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.Pod = strings.TrimSpace(request.Pod)
	request.PodIP = strings.TrimSpace(request.PodIP)
	request.Container = strings.TrimSpace(request.Container)
	if request.Namespace == "" || request.Pod == "" || request.PodIP == "" || session.Namespace != request.Namespace {
		return Request{}, nil, errors.New("pod SSH target must belong to the active Session namespace")
	}
	if !request.Ready {
		return Request{}, nil, errors.New("pod SSH target must be ready")
	}
	containers := normalizeContainers(request.Containers)
	if len(containers) == 0 {
		return Request{}, nil, errors.New("pod SSH target has no containers")
	}
	if request.Container == "" {
		request.Container = containers[0]
	}
	if !slices.Contains(containers, request.Container) {
		return Request{}, nil, fmt.Errorf(
			"container %q is not available in Pod %s/%s",
			request.Container,
			request.Namespace,
			request.Pod,
		)
	}
	return request, containers, nil
}

func (manager *Manager) reserveStart(profileID string, request Request) (string, *activeEndpoint, error) {
	reservation := profileID + "\x00" + request.Namespace + "\x00" + request.Pod
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.starting[reservation]; exists {
		return "", nil, errors.New("pod SSH endpoint is already active")
	}
	if existing := manager.findLocked(profileID, request.Namespace, request.Pod); existing != nil {
		return "", existing, nil
	}
	manager.starting[reservation] = struct{}{}
	return reservation, nil, nil
}

func (manager *Manager) reuseEndpoint(existing *activeEndpoint, container string) (Info, error) {
	info := existing.info
	command, err := manager.server.Command(info.ID, container)
	if err != nil {
		return Info{}, err
	}
	info.Container = container
	info.Command = command
	return info, nil
}

func (manager *Manager) enableEndpoint(
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
	containers []string,
	target localpodssh.Target,
) (*activeEndpoint, error) {
	baseInfo, err := manager.server.Enable(target)
	if err != nil {
		return nil, err
	}
	if manager.hostTCP == nil {
		return nil, errors.New("native PodIP SSH interception is unavailable")
	}
	if err := manager.hostTCP.SetHostTCPHandler(
		serverProfile.ID,
		func(host string, port uint16) (func(net.Conn), bool) {
			return manager.server.HostTCPForContext(serverProfile.ID, host, port)
		},
	); err != nil {
		return nil, err
	}
	return &activeEndpoint{
		profile: serverProfile, session: session, target: target,
		info: Info{
			ID: baseInfo.ID, ProfileID: serverProfile.ID, SessionID: session.ID,
			Namespace: request.Namespace, Pod: request.Pod, Container: request.Container,
			Containers: containers, PodIP: request.PodIP, Address: net.JoinHostPort(request.PodIP, "22"),
			Port: baseInfo.Port, Command: baseInfo.Command, State: podSSHSessionActive,
		},
	}, nil
}

func (manager *Manager) commitStart(reservation string, entry *activeEndpoint) {
	manager.mu.Lock()
	delete(manager.starting, reservation)
	manager.active[entry.info.ID] = entry
	manager.mu.Unlock()
}

func (manager *Manager) releaseStart(reservation, id string, committed bool) {
	manager.mu.Lock()
	delete(manager.starting, reservation)
	manager.mu.Unlock()
	if !committed {
		// Enable may not have reached the server; Disable is intentionally best effort.
		_ = manager.server.Disable(id)
	}
}

func endpointID(profileID string, request Request) string {
	return profileID + "/" + request.Namespace + "/" + request.Pod
}
