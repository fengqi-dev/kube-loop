package portforward

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/portforward/listener"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/traffic"
)

type TaskClient interface {
	CreatePortForward(
		context.Context,
		profile.Profile,
		remote.Session,
		remote.PortForwardSpec,
		string,
	) (remote.PortForwardTask, error)
	StopPortForward(context.Context, profile.Profile, remote.Session, string) (remote.PortForwardTask, error)
}

type DataPlane interface {
	Dialer(string) (traffic.Dialer, error)
}

type localForwards interface {
	StartResolved(context.Context, listener.Request, string, listener.TrafficDialer) (listener.Info, error)
	Stop(string) error
}

type Request struct {
	ProfileID  string `json:"profileId"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol,omitempty"`
	RemotePort uint16 `json:"remotePort"`
	LocalPort  uint16 `json:"localPort"`
}

type Info struct {
	ID          string `json:"id"`
	ProfileID   string `json:"profileId"`
	SessionID   string `json:"sessionId"`
	Namespace   string `json:"namespace"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	RemotePort  uint16 `json:"remotePort"`
	LocalPort   uint16 `json:"localPort"`
	Address     string `json:"address"`
	DialAddress string `json:"dialAddress"`
	State       string `json:"state"`
}

type activeForward struct {
	profile profile.Profile
	session remote.Session
	task    remote.PortForwardTask
	localID string
	info    Info
}

type Manager struct {
	client     TaskClient
	dataPlanes DataPlane
	locals     localForwards

	mu     sync.Mutex
	active map[string]*activeForward
}

func New(client TaskClient, dataPlanes DataPlane) (*Manager, error) {
	if client == nil || dataPlanes == nil {
		return nil, errors.New("port Forward Task client and Data Plane are required")
	}
	return &Manager{
		client: client, dataPlanes: dataPlanes, locals: listener.NewManager(),
		active: make(map[string]*activeForward),
	}, nil
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	validProfile := strings.TrimSpace(request.ProfileID) == serverProfile.ID
	if ctx == nil || !validProfile || session.State != portForwardSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	dialer, err := manager.dataPlanes.Dialer(serverProfile.ID)
	if err != nil {
		return Info{}, err
	}
	idempotencyKey := "port-forward:" + requestID()
	task, err := manager.client.CreatePortForward(ctx, serverProfile, session, remote.PortForwardSpec{
		Kind: request.Kind, Name: request.Name, Protocol: request.Protocol, RemotePort: request.RemotePort,
	}, idempotencyKey)
	if err != nil {
		return Info{}, err
	}
	local, err := manager.locals.StartResolved(ctx, listener.Request{
		Context: serverProfile.ID, Namespace: session.Namespace, Kind: task.Kind, Name: task.Name,
		Protocol: task.Protocol, RemotePort: task.RemotePort, LocalPort: request.LocalPort,
	}, task.DialAddress, dialer)
	if err != nil {
		_, stopErr := manager.client.StopPortForward(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(fmt.Errorf("start local Port Forward listener: %w", err), stopErr)
	}
	info := Info{
		ID:          task.ID,
		ProfileID:   serverProfile.ID,
		SessionID:   session.ID,
		Namespace:   session.Namespace,
		Kind:        task.Kind,
		Name:        task.Name,
		Protocol:    task.Protocol,
		RemotePort:  task.RemotePort,
		LocalPort:   local.LocalPort,
		Address:     local.Address,
		DialAddress: task.DialAddress,
		State:       portForwardSessionActive,
	}
	manager.mu.Lock()
	if _, exists := manager.active[task.ID]; exists {
		manager.mu.Unlock()
		_ = manager.locals.Stop(local.ID)
		_, stopErr := manager.client.StopPortForward(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(errors.New("port Forward Task is already active locally"), stopErr)
	}
	manager.active[task.ID] = &activeForward{
		profile: serverProfile, session: session, task: task, localID: local.ID, info: info,
	}
	manager.mu.Unlock()
	return info, nil
}

func (manager *Manager) Stop(ctx context.Context, profileID, taskID string) error {
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry != nil && entry.profile.ID == profileID {
		delete(manager.active, taskID)
	} else {
		entry = nil
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("port Forward is not active locally")
	}
	localErr := manager.locals.Stop(entry.localID)
	_, remoteErr := manager.client.StopPortForward(ctx, entry.profile, entry.session, entry.task.ID)
	return errors.Join(localErr, remoteErr)
}

func (manager *Manager) List(profileID string) []Info {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]Info, 0, len(manager.active))
	for _, entry := range manager.active {
		if profileID == "" || entry.profile.ID == profileID {
			items = append(items, entry.info)
		}
	}
	slices.SortFunc(items, func(left, right Info) int { return strings.Compare(left.ID, right.ID) })
	return items
}

func (manager *Manager) StopProfile(ctx context.Context, profileID string) error {
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID {
			ids = append(ids, id)
		}
	}
	manager.mu.Unlock()
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(ctx, profileID, id))
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("port Forward shutdown context is required")
	}
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
			result = errors.Join(result, manager.Stop(ctx, entry.profile.ID, id))
		}
	}
	return result
}

func requestID() string {
	return uuid.NewString()
}
