package filetransfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

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
	manager.persistMu.Lock()
	defer manager.persistMu.Unlock()
	return manager.persist(cloneTasks(manager.tasks))
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

func (manager *Manager) persist(tasks map[string]Task) error {
	if manager.statePath == "" {
		return nil
	}
	state := persistedState{Version: stateVersion, Tasks: make([]Task, 0, len(tasks))}
	for _, task := range tasks {
		state.Tasks = append(state.Tasks, task)
	}
	slices.SortFunc(state.Tasks, func(left, right Task) int { return strings.Compare(left.ID, right.ID) })
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.New("encode file transfer state")
	}
	contents = append(contents, '\n')
	writeFile := manager.writeFile
	if writeFile == nil {
		writeFile = fsatomic.WriteFile
	}
	return writeFile(manager.statePath, contents, 0o700, 0o600)
}

func cloneTasks(tasks map[string]Task) map[string]Task {
	return maps.Clone(tasks)
}
