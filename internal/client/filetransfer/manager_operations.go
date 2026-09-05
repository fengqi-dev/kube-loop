package filetransfer

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) Start(
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Task, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	select {
	case <-manager.ctx.Done():
		return Task{}, errors.New("file transfer manager is shut down")
	default:
	}
	request.ProfileID = strings.TrimSpace(request.ProfileID)
	request.Direction = strings.ToLower(strings.TrimSpace(request.Direction))
	request.Kind = strings.ToLower(strings.TrimSpace(request.Kind))
	request.Pod = strings.TrimSpace(request.Pod)
	request.Container = strings.TrimSpace(request.Container)
	request.RemotePath = strings.TrimSpace(request.RemotePath)
	if request.ProfileID == "" || request.ProfileID != serverProfile.ID || session.State != fileTransferSessionActive ||
		session.ID == "" || session.Namespace == "" {
		return Task{}, errors.New("active Profile Session is required for file transfer")
	}
	if request.Direction != fileTransferDirectionUpload && request.Direction != fileTransferDirectionDownload {
		return Task{}, errors.New("file transfer direction must be upload or download")
	}
	if request.Kind != fileTransferKindFile && request.Kind != fileTransferKindDirectory {
		return Task{}, errors.New("file transfer kind must be file or directory")
	}
	if request.Pod == "" {
		return Task{}, errors.New("file transfer Pod is required")
	}
	if err := validateManagerRemotePath(request.RemotePath); err != nil {
		return Task{}, err
	}
	localPath, err := cleanLocalPath(request.LocalPath)
	if err != nil {
		return Task{}, err
	}
	request.LocalPath = localPath
	now := manager.now().UTC()
	task := Task{
		ID: uuid.NewString(), ProfileID: serverProfile.ID, SessionID: session.ID, Namespace: session.Namespace,
		Direction: request.Direction, Kind: request.Kind, Pod: request.Pod, Container: request.Container,
		LocalPath: request.LocalPath, RemotePath: request.RemotePath, Overwrite: request.Overwrite,
		Status: StatusQueued, CreatedAt: now, UpdatedAt: now,
	}
	if request.Kind == fileTransferKindFile {
		if request.Direction == fileTransferDirectionUpload {
			task.ResumeID = task.ID
		} else {
			task.TemporaryPath = downloadTemporaryPath(request.LocalPath, task.ID)
		}
	}
	transferContext, cancel := context.WithCancel(manager.ctx)
	entry := &activeTransfer{
		profile: serverProfile, session: session, request: request, cancel: cancel, done: make(chan struct{}),
		resumeID: task.ResumeID, temporaryPath: task.TemporaryPath,
	}
	if err := manager.launch(transferContext, task, entry); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (manager *Manager) Resume(
	serverProfile profile.Profile,
	session remote.Session,
	profileID,
	taskID string,
) (Task, error) {
	manager.lifecycle.RLock()
	defer manager.lifecycle.RUnlock()
	select {
	case <-manager.ctx.Done():
		return Task{}, errors.New("file transfer manager is shut down")
	default:
	}
	profileID, taskID = strings.TrimSpace(profileID), strings.TrimSpace(taskID)
	validProfile := profileID != "" && profileID == serverProfile.ID
	validSession := session.State == fileTransferSessionActive && session.ID != "" && session.Namespace != ""
	if !validProfile || !validSession {
		return Task{}, errors.New("active Profile Session is required to resume file transfer")
	}
	manager.persistMu.Lock()
	manager.mu.Lock()
	task, exists := manager.tasks[taskID]
	if !exists || task.ProfileID != profileID || manager.active[taskID] != nil ||
		(task.Status != StatusInterrupted && task.Status != StatusFailed) {
		manager.mu.Unlock()
		manager.persistMu.Unlock()
		return Task{}, errors.New("file transfer is not resumable")
	}
	nextTasks := maps.Clone(manager.tasks)
	manager.mu.Unlock()
	request := Request{
		ProfileID: profileID, Direction: task.Direction, Kind: task.Kind, Pod: task.Pod, Container: task.Container,
		LocalPath: task.LocalPath, RemotePath: task.RemotePath, Overwrite: task.Overwrite,
	}
	if task.Kind == fileTransferKindFile && task.Direction == fileTransferDirectionUpload && task.ResumeID == "" {
		task.ResumeID = task.ID
	}
	if task.Kind == fileTransferKindFile && task.Direction == fileTransferDirectionDownload &&
		task.TemporaryPath == "" {
		task.TemporaryPath = downloadTemporaryPath(task.LocalPath, task.ID)
	}
	now := manager.now().UTC()
	task.SessionID, task.Namespace = session.ID, session.Namespace
	task.Status, task.Error, task.CompletedAt = StatusQueued, "", nil
	task.UpdatedAt = now
	transferContext, cancel := context.WithCancel(manager.ctx)
	entry := &activeTransfer{
		profile: serverProfile, session: session, request: request, cancel: cancel, done: make(chan struct{}),
		resumeID: task.ResumeID, temporaryPath: task.TemporaryPath,
	}
	if err := manager.launchPrepared(transferContext, task, entry, nextTasks); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (manager *Manager) Cancel(profileID, taskID string) error {
	manager.mu.Lock()
	entry := manager.active[taskID]
	task, exists := manager.tasks[taskID]
	if entry == nil || !exists || task.ProfileID != profileID {
		manager.mu.Unlock()
		return errors.New("file transfer is not active")
	}
	manager.mu.Unlock()
	entry.cancel()
	return nil
}

func (manager *Manager) StopProfile(profileID string) error {
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	entries := make([]*activeTransfer, 0)
	for taskID, entry := range manager.active {
		if manager.tasks[taskID].ProfileID == profileID {
			entries = append(entries, entry)
		}
	}
	manager.mu.Unlock()
	for _, entry := range entries {
		entry.cancel()
	}
	for _, entry := range entries {
		<-entry.done
	}
	return nil
}

func (manager *Manager) ClearHistory(profileID string) error {
	manager.persistMu.Lock()
	defer manager.persistMu.Unlock()
	manager.mu.Lock()
	removed := make([]string, 0)
	nextTasks := maps.Clone(manager.tasks)
	active := make(map[string]struct{}, len(manager.active))
	for id := range manager.active {
		active[id] = struct{}{}
	}
	manager.mu.Unlock()
	for id, task := range nextTasks {
		_, isActive := active[id]
		if (profileID == "" || task.ProfileID == profileID) && !isActive {
			if task.TemporaryPath != "" {
				removed = append(removed, task.TemporaryPath)
			}
			delete(nextTasks, id)
		}
	}
	if err := manager.persist(nextTasks); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.tasks = nextTasks
	manager.mu.Unlock()
	for _, filename := range removed {
		_ = os.Remove(filename)
	}
	return nil
}

func (manager *Manager) Shutdown() error {
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.cancel()
	manager.wg.Wait()
	manager.persistMu.Lock()
	defer manager.persistMu.Unlock()
	manager.mu.Lock()
	tasks := maps.Clone(manager.tasks)
	manager.mu.Unlock()
	return manager.persist(tasks)
}
