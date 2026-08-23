package exec

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
)

const (
	EventStdout = "stdout"
	EventStderr = "stderr"
	EventExit   = "exit"
	EventError  = "error"
)

type Event struct {
	ProfileID string `json:"profileId"`
	TaskID    string `json:"taskId"`
	Type      string `json:"type"`
	Data      string `json:"data,omitempty"`
	ExitCode  uint32 `json:"exitCode,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ManagerConfig struct {
	OnEvent func(Event)
}

type managedStream struct {
	profileID string
	stream    *Stream
	cancel    context.CancelFunc
}

type Manager struct {
	client  Client
	onEvent func(Event)

	lifecycle sync.RWMutex
	mu        sync.Mutex
	active    map[string]*managedStream
}

func NewManager(client Client, config ManagerConfig) (*Manager, error) {
	if client == nil {
		return nil, errors.New("pod exec client is required")
	}
	if config.OnEvent == nil {
		config.OnEvent = func(Event) {}
	}
	return &Manager{client: client, onEvent: config.OnEvent, active: make(map[string]*managedStream)}, nil
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	spec remote.ExecSpec,
) (remote.ExecTask, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	if ctx == nil {
		return remote.ExecTask{}, errors.New("pod exec context is required")
	}
	stream, err := Start(ctx, manager.client, serverProfile, session, spec)
	if err != nil {
		return remote.ExecTask{}, err
	}
	streamContext, cancel := context.WithCancel(ctx)
	entry := &managedStream{profileID: serverProfile.ID, stream: stream, cancel: cancel}
	manager.mu.Lock()
	if _, exists := manager.active[stream.Task().ID]; exists {
		manager.mu.Unlock()
		cancel()
		_ = stream.Close()
		return remote.ExecTask{}, errors.New("pod exec Task is already active")
	}
	manager.active[stream.Task().ID] = entry
	manager.mu.Unlock()
	go manager.read(streamContext, stream.Task().ID, entry)
	return stream.Task(), nil
}

func (manager *Manager) Write(ctx context.Context, profileID, taskID string, data []byte) error {
	entry, err := manager.get(profileID, taskID)
	if err != nil {
		return err
	}
	return entry.stream.WriteStdin(ctx, data)
}

func (manager *Manager) Resize(ctx context.Context, profileID, taskID string, width, height uint16) error {
	entry, err := manager.get(profileID, taskID)
	if err != nil {
		return err
	}
	return entry.stream.Resize(ctx, width, height)
}

func (manager *Manager) Stop(profileID, taskID string) error {
	entry, err := manager.remove(profileID, taskID)
	if err != nil {
		return err
	}
	entry.cancel()
	return entry.stream.Close()
}

func (manager *Manager) StopProfile(profileID string) error {
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	entries := make([]*managedStream, 0)
	for taskID, entry := range manager.active {
		if entry.profileID == profileID {
			delete(manager.active, taskID)
			entries = append(entries, entry)
		}
	}
	manager.mu.Unlock()
	var result error
	for _, entry := range entries {
		entry.cancel()
		result = errors.Join(result, entry.stream.Close())
	}
	return result
}

func (manager *Manager) Shutdown() error {
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	entries := make([]*managedStream, 0, len(manager.active))
	for taskID, entry := range manager.active {
		delete(manager.active, taskID)
		entries = append(entries, entry)
	}
	manager.mu.Unlock()
	var result error
	for _, entry := range entries {
		entry.cancel()
		result = errors.Join(result, entry.stream.Close())
	}
	return result
}

func (manager *Manager) read(ctx context.Context, taskID string, entry *managedStream) {
	defer manager.cleanup(taskID, entry)
	for {
		frame, err := entry.stream.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				manager.onEvent(Event{
					ProfileID: entry.profileID,
					TaskID:    taskID,
					Type:      EventError,
					Error:     "Pod exec stream ended unexpectedly",
				})
			}
			return
		}
		switch frame.Type {
		case execstream.Stdout:
			manager.emitOutput(entry.profileID, taskID, EventStdout, frame.Payload)
		case execstream.Stderr:
			manager.emitOutput(entry.profileID, taskID, EventStderr, frame.Payload)
		case execstream.Exit:
			status, decodeErr := execstream.DecodeExit(frame)
			if decodeErr != nil {
				manager.onEvent(Event{
					ProfileID: entry.profileID,
					TaskID:    taskID,
					Type:      EventError,
					Error:     "Gateway returned an invalid Pod exec exit status",
				})
				return
			}
			manager.cleanup(taskID, entry)
			manager.onEvent(Event{
				ProfileID: entry.profileID, TaskID: taskID, Type: EventExit,
				ExitCode: status.Code, Cancelled: status.Cancelled, Error: status.Error,
			})
			return
		}
	}
}

func (manager *Manager) emitOutput(profileID, taskID, eventType string, payload []byte) {
	manager.onEvent(Event{
		ProfileID: profileID, TaskID: taskID, Type: eventType,
		Data: base64.StdEncoding.EncodeToString(payload),
	})
}

func (manager *Manager) get(profileID, taskID string) (*managedStream, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[taskID]
	if entry == nil || entry.profileID != profileID {
		return nil, errors.New("pod exec stream is not active")
	}
	return entry, nil
}

func (manager *Manager) remove(profileID, taskID string) (*managedStream, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry := manager.active[taskID]
	if entry == nil || entry.profileID != profileID {
		return nil, errors.New("pod exec stream is not active")
	}
	delete(manager.active, taskID)
	return entry, nil
}

func (manager *Manager) cleanup(taskID string, entry *managedStream) {
	manager.mu.Lock()
	if manager.active[taskID] == entry {
		delete(manager.active, taskID)
	}
	manager.mu.Unlock()
	entry.cancel()
	_ = entry.stream.Close()
}
