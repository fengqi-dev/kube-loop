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

var ErrClosed = errors.New("port Forward manager is closed")

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

	lifecycle sync.RWMutex
	closed    bool
	mu        sync.Mutex
	active    map[string]*activeForward
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
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if manager.closed {
		return Info{}, ErrClosed
	}
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
		_, stopErr := deletePortForward(ctx, manager.client, serverProfile, session, task.ID)
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
		_, stopErr := deletePortForward(ctx, manager.client, serverProfile, session, task.ID)
		return Info{}, errors.Join(errors.New("port Forward Task is already active locally"), stopErr)
	}
	manager.active[task.ID] = &activeForward{
		profile: serverProfile, session: session, task: task, localID: local.ID, info: info,
	}
	manager.mu.Unlock()
	return info, nil
}

func (manager *Manager) Pause(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("port Forward pause context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID ||
		(entry.info.State != "" && entry.info.State != portForwardSessionActive) {
		entry = nil
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("port Forward is not active locally")
	}
	_, remoteErr := pausePortForward(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	localErr := manager.locals.Stop(entry.localID)
	manager.mu.Lock()
	if manager.active[taskID] == entry {
		entry.localID = ""
		entry.info.State = "paused"
	}
	manager.mu.Unlock()
	return errors.Join(localErr, remoteErr)
}

func (manager *Manager) Resume(ctx context.Context, profileID, taskID string) (Info, error) {
	if ctx == nil {
		return Info{}, errors.New("port Forward resume context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID || entry.info.State != "paused" {
		entry = nil
	}
	manager.mu.Unlock()
	if entry == nil {
		return Info{}, errors.New("port Forward is not paused locally")
	}
	task, err := resumePortForward(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	if err != nil {
		return Info{}, err
	}
	dialer, err := manager.dataPlanes.Dialer(entry.profile.ID)
	if err != nil {
		return Info{}, err
	}
	local, err := manager.locals.StartResolved(ctx, listener.Request{
		Context: entry.profile.ID, Namespace: entry.session.Namespace, Kind: task.Kind, Name: task.Name,
		Protocol: task.Protocol, RemotePort: task.RemotePort, LocalPort: entry.info.LocalPort,
	}, task.DialAddress, dialer)
	if err != nil {
		_, pauseErr := manager.client.StopPortForward(ctx, entry.profile, entry.session, task.ID)
		return Info{}, errors.Join(err, pauseErr)
	}
	manager.mu.Lock()
	entry.task, entry.localID = task, local.ID
	entry.info.State, entry.info.Address, entry.info.LocalPort = portForwardSessionActive, local.Address, local.LocalPort
	entry.info.DialAddress = task.DialAddress
	info := entry.info
	manager.mu.Unlock()
	return info, nil
}

func (manager *Manager) Delete(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("port Forward delete context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	manager.mu.Unlock()
	if entry == nil || entry.profile.ID != profileID {
		return errors.New("port Forward is not managed locally")
	}
	var pauseErr error
	if entry.info.State == portForwardSessionActive {
		pauseErr = manager.Pause(ctx, profileID, taskID)
	}
	_, deleteErr := deletePortForward(ctx, manager.client, entry.profile, entry.session, entry.task.ID)
	if deleteErr == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.mu.Unlock()
	}
	return errors.Join(pauseErr, deleteErr)
}

func resumePortForward(
	ctx context.Context, client TaskClient, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.PortForwardTask, error) {
	lifecycle, ok := client.(interface {
		ResumePortForward(context.Context, profile.Profile, remote.Session, string) (remote.PortForwardTask, error)
	})
	if !ok {
		return remote.PortForwardTask{}, errors.New("port Forward resume is unavailable")
	}
	return lifecycle.ResumePortForward(ctx, serverProfile, session, taskID)
}

func pausePortForward(
	ctx context.Context, client TaskClient, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.PortForwardTask, error) {
	pauser, ok := client.(interface {
		PausePortForward(context.Context, profile.Profile, remote.Session, string) (remote.PortForwardTask, error)
	})
	if !ok {
		return client.StopPortForward(ctx, serverProfile, session, taskID)
	}
	return pauser.PausePortForward(ctx, serverProfile, session, taskID)
}

func deletePortForward(
	ctx context.Context, client TaskClient, serverProfile profile.Profile, session remote.Session, taskID string,
) (remote.PortForwardTask, error) {
	lifecycle, ok := client.(interface {
		DeletePortForward(context.Context, profile.Profile, remote.Session, string) (remote.PortForwardTask, error)
	})
	if !ok {
		return client.StopPortForward(ctx, serverProfile, session, taskID)
	}
	return lifecycle.DeletePortForward(ctx, serverProfile, session, taskID)
}

func (manager *Manager) Stop(ctx context.Context, profileID, taskID string) error {
	err := manager.Pause(ctx, profileID, taskID)
	if err == nil {
		manager.mu.Lock()
		delete(manager.active, taskID)
		manager.mu.Unlock()
	}
	return err
}

func (manager *Manager) StopProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return errors.New("port Forward stop Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID && (entry.info.State == "" || entry.info.State == portForwardSessionActive) {
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

func (manager *Manager) PauseProfile(ctx context.Context, profileID string) error {
	if ctx == nil {
		return errors.New("port Forward pause Profile context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
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
		result = errors.Join(result, manager.Pause(ctx, profileID, id))
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("port Forward shutdown context is required")
	}
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.closed = true
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
			if entry.info.State == "" || entry.info.State == portForwardSessionActive {
				result = errors.Join(result, manager.Stop(ctx, entry.profile.ID, id))
			}
		}
	}
	return result
}

func requestID() string {
	return uuid.NewString()
}
