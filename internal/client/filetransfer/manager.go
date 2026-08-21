package filetransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

const (
	StatusQueued      = "queued"
	StatusPreparing   = "preparing"
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusCancelled   = "cancelled"
	StatusInterrupted = "interrupted"

	stateVersion       = 1
	defaultMaximumSize = uint64(1 << 30)
)

type Request struct {
	ProfileID  string `json:"profileId"`
	Direction  string `json:"direction"`
	Kind       string `json:"kind"`
	Pod        string `json:"pod"`
	Container  string `json:"container,omitempty"`
	LocalPath  string `json:"localPath"`
	RemotePath string `json:"remotePath"`
	Overwrite  bool   `json:"overwrite,omitempty"`
}

type Task struct {
	ID            string     `json:"id"`
	ProfileID     string     `json:"profileId"`
	SessionID     string     `json:"sessionId"`
	Namespace     string     `json:"namespace"`
	Direction     string     `json:"direction"`
	Kind          string     `json:"kind"`
	Pod           string     `json:"pod"`
	Container     string     `json:"container,omitempty"`
	LocalPath     string     `json:"localPath"`
	RemotePath    string     `json:"remotePath"`
	Overwrite     bool       `json:"overwrite,omitempty"`
	Status        string     `json:"status"`
	TotalBytes    uint64     `json:"totalBytes,omitempty"`
	DoneBytes     uint64     `json:"doneBytes,omitempty"`
	Checksum      string     `json:"checksum,omitempty"`
	ResumeID      string     `json:"resumeId,omitempty"`
	TemporaryPath string     `json:"temporaryPath,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"               ts_type:"string"`
	UpdatedAt     time.Time  `json:"updatedAt"               ts_type:"string"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"   ts_type:"string"`
}

type Config struct {
	StatePath    string
	TemporaryDir string
	MaximumBytes uint64
	Now          func() time.Time
	OnEvent      func(Task)
}

type persistedState struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

type activeTransfer struct {
	profile       profile.Profile
	session       remote.Session
	request       Request
	cancel        context.CancelFunc
	done          chan struct{}
	resumeID      string
	temporaryPath string
}

type Manager struct {
	client       Client
	statePath    string
	temporaryDir string
	maximumBytes uint64
	now          func() time.Time
	onEvent      func(Task)
	ctx          context.Context
	cancel       context.CancelFunc

	mu     sync.Mutex
	tasks  map[string]Task
	active map[string]*activeTransfer
	wg     sync.WaitGroup
}

func NewManager(client Client, config Config) (*Manager, error) {
	if client == nil {
		return nil, errors.New("file transfer client is required")
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = defaultMaximumSize
	}
	if config.MaximumBytes < filestream.MaximumData || config.MaximumBytes > 1<<40 {
		return nil, errors.New("file transfer maximum size must be between 256 KiB and 1 TiB")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OnEvent == nil {
		config.OnEvent = func(Task) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{
		client:       client,
		statePath:    strings.TrimSpace(config.StatePath),
		temporaryDir: strings.TrimSpace(config.TemporaryDir),
		maximumBytes: config.MaximumBytes,
		now:          config.Now,
		onEvent:      config.OnEvent,
		ctx:          ctx,
		cancel:       cancel,
		tasks:        make(map[string]Task),
		active:       make(map[string]*activeTransfer),
	}
	if manager.statePath != "" {
		if err := fsatomic.CleanupTemps(manager.statePath); err != nil {
			cancel()
			return nil, fmt.Errorf("clean file transfer state: %w", err)
		}
	}
	if err := manager.load(); err != nil {
		cancel()
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) Start(
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Task, error) {
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
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.active[task.ID] = entry
	if err := manager.persistLocked(); err != nil {
		delete(manager.tasks, task.ID)
		delete(manager.active, task.ID)
		manager.mu.Unlock()
		cancel()
		return Task{}, err
	}
	manager.mu.Unlock()
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
	manager.mu.Lock()
	task, exists := manager.tasks[taskID]
	if !exists || task.ProfileID != profileID || manager.active[taskID] != nil ||
		(task.Status != StatusInterrupted && task.Status != StatusFailed) {
		manager.mu.Unlock()
		return Task{}, errors.New("file transfer is not resumable")
	}
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
	manager.tasks[task.ID], manager.active[task.ID] = task, entry
	if err := manager.persistLocked(); err != nil {
		delete(manager.active, task.ID)
		manager.mu.Unlock()
		cancel()
		return Task{}, err
	}
	manager.mu.Unlock()
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
	manager.mu.Lock()
	removed := make([]string, 0)
	for id, task := range manager.tasks {
		if (profileID == "" || task.ProfileID == profileID) && manager.active[id] == nil {
			if task.TemporaryPath != "" {
				removed = append(removed, task.TemporaryPath)
			}
			delete(manager.tasks, id)
		}
	}
	err := manager.persistLocked()
	manager.mu.Unlock()
	for _, filename := range removed {
		_ = os.Remove(filename)
	}
	return err
}

func (manager *Manager) Shutdown() error {
	manager.cancel()
	manager.wg.Wait()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.persistLocked()
}

func (manager *Manager) run(ctx context.Context, taskID string, entry *activeTransfer) {
	defer manager.wg.Done()
	defer close(entry.done)
	manager.update(taskID, func(task *Task) { task.Status = StatusPreparing })
	var result filestream.TransferResult
	var err error
	if entry.request.Direction == fileTransferDirectionUpload {
		result, err = manager.runUpload(ctx, taskID, entry)
	} else {
		result, err = manager.runDownload(ctx, taskID, entry)
	}
	manager.finish(taskID, result, err, ctx.Err() != nil)
}

func (manager *Manager) progress(taskID string, status filestream.ProgressStatus) {
	manager.update(taskID, func(task *Task) {
		task.Status = StatusRunning
		task.DoneBytes = status.Transferred
		if status.Total != 0 {
			task.TotalBytes = status.Total
		}
	})
}

func (manager *Manager) update(taskID string, mutate func(*Task)) {
	manager.mu.Lock()
	task, exists := manager.tasks[taskID]
	if !exists {
		manager.mu.Unlock()
		return
	}
	mutate(&task)
	task.UpdatedAt = manager.now().UTC()
	manager.tasks[taskID] = task
	_ = manager.persistLocked()
	manager.mu.Unlock()
	manager.onEvent(task)
}

func (manager *Manager) finish(taskID string, result filestream.TransferResult, transferErr error, cancelled bool) {
	manager.mu.Lock()
	task, exists := manager.tasks[taskID]
	if !exists {
		manager.mu.Unlock()
		return
	}
	delete(manager.active, taskID)
	now := manager.now().UTC()
	task.UpdatedAt = now
	task.CompletedAt = &now
	if result.Transferred > task.DoneBytes {
		task.DoneBytes = result.Transferred
	}
	if result.HasChecksum {
		task.Checksum = filestream.FormatChecksum(result.Checksum)
	}
	cleanup := ""
	switch {
	case cancelled && manager.ctx.Err() != nil:
		task.Status = StatusInterrupted
		task.Error = "application stopped before the transfer completed"
	case cancelled || errors.Is(transferErr, context.Canceled):
		task.Status = StatusCancelled
		task.Error = ""
		cleanup = task.TemporaryPath
	case transferErr != nil:
		task.Status = StatusFailed
		task.Error = transferErr.Error()
	default:
		task.Status = StatusCompleted
		task.Error = ""
	}
	manager.tasks[taskID] = task
	_ = manager.persistLocked()
	manager.mu.Unlock()
	if cleanup != "" {
		_ = os.Remove(cleanup)
	}
	manager.onEvent(task)
}

func (manager *Manager) load() error {
	if manager.statePath == "" {
		return nil
	}
	contents, err := os.ReadFile(manager.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read file transfer state: %w", err)
	}
	var state persistedState
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil || state.Version != stateVersion {
		return errors.New("file transfer state is invalid or unsupported")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("file transfer state is invalid or unsupported")
	}
	now := manager.now().UTC()
	for _, task := range state.Tasks {
		if err := normalizePersistedTask(&task); err != nil {
			continue
		}
		if task.Status == StatusQueued || task.Status == StatusPreparing || task.Status == StatusRunning {
			task.Status = StatusInterrupted
			task.Error = "application stopped before the transfer completed"
			task.UpdatedAt = now
			task.CompletedAt = &now
		}
		manager.tasks[task.ID] = task
	}
	return manager.persistLocked()
}

func normalizePersistedTask(task *Task) error {
	if task == nil {
		return errors.New("file transfer Task is nil")
	}
	_, idErr := uuid.Parse(task.ID)
	invalidIdentity := idErr != nil || strings.TrimSpace(task.ProfileID) == ""
	if invalidIdentity || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() {
		return errors.New("file transfer Task identity is invalid")
	}
	if task.Direction != fileTransferDirectionUpload && task.Direction != fileTransferDirectionDownload {
		return errors.New("file transfer Task direction is invalid")
	}
	if task.Kind != fileTransferKindFile && task.Kind != fileTransferKindDirectory ||
		strings.TrimSpace(task.Pod) == "" {
		return errors.New("file transfer Task target is invalid")
	}
	localPath, err := cleanLocalPath(task.LocalPath)
	if err != nil || localPath != task.LocalPath || validateManagerRemotePath(task.RemotePath) != nil {
		return errors.New("file transfer Task path is invalid")
	}
	validStatus := task.Status == StatusQueued || task.Status == StatusPreparing || task.Status == StatusRunning
	validStatus = validStatus || task.Status == StatusCompleted || task.Status == StatusFailed
	validStatus = validStatus || task.Status == StatusCancelled || task.Status == StatusInterrupted
	if !validStatus {
		return errors.New("file transfer Task status is invalid")
	}
	if err := normalizePersistedResume(task); err != nil {
		return err
	}
	return normalizePersistedTemporaryPath(task)
}

func normalizePersistedResume(task *Task) error {
	if task.Kind == fileTransferKindFile && task.Direction == fileTransferDirectionUpload {
		if task.ResumeID == "" {
			task.ResumeID = task.ID
		}
		if task.ResumeID != task.ID {
			return errors.New("file upload Resume ID is invalid")
		}
	} else if task.ResumeID != "" {
		return errors.New("file transfer Task has an unexpected Resume ID")
	}
	return nil
}

func normalizePersistedTemporaryPath(task *Task) error {
	if task.Kind == fileTransferKindFile && task.Direction == fileTransferDirectionDownload {
		expected := downloadTemporaryPath(task.LocalPath, task.ID)
		if task.TemporaryPath == "" {
			task.TemporaryPath = expected
		}
		if task.TemporaryPath != expected {
			return errors.New("file download temporary path is invalid")
		}
	} else if task.TemporaryPath != "" {
		return errors.New("file transfer Task has an unexpected temporary path")
	}
	return nil
}

func (manager *Manager) persistLocked() error {
	if manager.statePath == "" {
		return nil
	}
	state := persistedState{Version: stateVersion, Tasks: make([]Task, 0, len(manager.tasks))}
	for _, task := range manager.tasks {
		state.Tasks = append(state.Tasks, task)
	}
	slices.SortFunc(state.Tasks, func(left, right Task) int { return strings.Compare(left.ID, right.ID) })
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("encode file transfer state")
	}
	contents = append(contents, '\n')
	return fsatomic.WriteFile(manager.statePath, contents, 0o700, 0o600)
}
