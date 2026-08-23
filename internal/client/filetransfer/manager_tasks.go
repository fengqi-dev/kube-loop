package filetransfer

import (
	"context"
	"errors"
	"path"
	"slices"
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
	manager.persistMu.Lock()
	manager.mu.Lock()
	nextTasks := cloneTasks(manager.tasks)
	manager.mu.Unlock()
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

func validateManagerRemotePath(value string) error {
	unsafeForm := value == "" || len(value) > 4096 || value[0] != '/' || value == "/"
	if unsafeForm || strings.Contains(value, "\\") || path.Clean(value) != value {
		return errors.New("file transfer remote path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("file transfer remote path is invalid")
		}
	}
	return nil
}

func (manager *Manager) List(profileID string) []Task {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]Task, 0, len(manager.tasks))
	for _, task := range manager.tasks {
		if profileID == "" || task.ProfileID == profileID {
			items = append(items, task)
		}
	}
	slices.SortFunc(items, func(left, right Task) int {
		if order := right.CreatedAt.Compare(left.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	return items
}

func (manager *Manager) task(taskID string) Task {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.tasks[taskID]
}
