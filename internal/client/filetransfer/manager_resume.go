package filetransfer

import (
	"context"
	"errors"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

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
	nextTasks := cloneTasks(manager.tasks)
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
	nextTasks[task.ID] = task
	if err := manager.persist(nextTasks); err != nil {
		manager.persistMu.Unlock()
		cancel()
		return Task{}, err
	}
	manager.mu.Lock()
	manager.tasks = nextTasks
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	manager.persistMu.Unlock()
	manager.onEvent(task)
	manager.wg.Add(1)
	go manager.run(transferContext, task.ID, entry)
	return task, nil
}
