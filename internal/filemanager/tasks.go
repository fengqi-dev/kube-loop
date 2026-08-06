package filemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

func (m *Manager) Start(ctx context.Context, request TransferRequest) (TransferTask, error) {
	if err := validateTarget(request.Target); err != nil {
		return TransferTask{}, err
	}
	if request.Direction != DirectionUpload && request.Direction != DirectionDownload {
		return TransferTask{}, errors.New("direction must be upload or download")
	}
	var source FileEntry
	var destination string
	var err error
	switch request.Direction {
	case DirectionUpload:
		source, err = localStat(request.SourcePath)
		if err == nil {
			destinationRoot, cleanErr := cleanRemotePath(request.DestinationDir)
			if cleanErr != nil {
				err = cleanErr
			} else {
				destination = path.Join(destinationRoot, source.Name)
			}
		}
	case DirectionDownload:
		sourcePath, cleanErr := cleanRemotePath(request.SourcePath)
		if cleanErr != nil {
			err = cleanErr
			break
		}
		source, err = m.remoteStat(ctx, request.Target, sourcePath)
		if err == nil {
			destinationRoot, absErr := filepath.Abs(filepath.Clean(request.DestinationDir))
			if absErr != nil {
				err = absErr
			} else {
				destination = filepath.Join(destinationRoot, path.Base(sourcePath))
			}
		}
	}
	if err != nil {
		return TransferTask{}, err
	}
	if source.Dir {
		if request.Direction == DirectionUpload && isLocalRoot(source.Path) {
			return TransferTask{}, errors.New("cannot upload a filesystem root")
		}
		if request.Direction == DirectionDownload && source.Path == "/" {
			return TransferTask{}, errors.New("cannot download the container root")
		}
	}
	if source.Dir && request.Direction == DirectionUpload {
		source.Size, err = localTreeSize(source.Path)
		if err != nil {
			return TransferTask{}, err
		}
	}
	if !request.Overwrite {
		if request.Direction == DirectionDownload {
			if _, statErr := os.Stat(destination); statErr == nil {
				return TransferTask{}, fmt.Errorf("destination %s already exists", destination)
			}
		} else if _, statErr := m.remoteStat(ctx, request.Target, destination); statErr == nil {
			return TransferTask{}, fmt.Errorf("destination %s already exists", destination)
		}
	}
	now := time.Now()
	id := fmt.Sprintf("transfer-%d-%d", now.UnixMilli(), m.nextID.Add(1))
	tempPath := destination + ".kubeloop-" + id + ".part"
	if request.Direction == DirectionUpload {
		tempPath = destination + ".kubeloop-upload-" + id + ".part"
	}
	if source.Dir {
		tempPath += "dir"
	}
	task := TransferTask{
		ID: id, Direction: request.Direction, Target: request.Target,
		SourcePath: source.Path, DestinationPath: destination, TempPath: tempPath,
		Directory: source.Dir, Status: StatusQueued, TotalBytes: source.Size,
		SourceModTime: source.ModTime,
		Overwrite:     request.Overwrite, CreatedAt: now, UpdatedAt: now,
	}
	m.mu.Lock()
	m.tasks[id] = &task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(task)
	m.launch(id)
	return task, nil
}

func (m *Manager) ListTransfers() []TransferTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]TransferTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		items = append(items, *task)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func (m *Manager) Pause(id string) error {
	return m.stop(id, StatusPaused)
}

func (m *Manager) Cancel(id string) error {
	return m.stop(id, StatusCancelled)
}

func (m *Manager) stop(id string, status TaskStatus) error {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return fmt.Errorf("transfer %q not found", id)
	}
	if task.Status == StatusCompleted || task.Status == StatusCancelled {
		m.mu.Unlock()
		return nil
	}
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
	}
	task.Status = status
	task.UpdatedAt = time.Now()
	copy := *task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(copy)
	return nil
}

func (m *Manager) Resume(id string) error {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return fmt.Errorf("transfer %q not found", id)
	}
	switch task.Status {
	case StatusPaused, StatusFailed, StatusStale:
	default:
		m.mu.Unlock()
		return fmt.Errorf("transfer %q cannot resume from %s", id, task.Status)
	}
	task.Status = StatusQueued
	task.Error = ""
	task.UpdatedAt = time.Now()
	copy := *task
	_ = m.saveLocked()
	m.mu.Unlock()
	m.emit(copy)
	m.launch(id)
	return nil
}

func (m *Manager) ClearHistory() error {
	m.mu.Lock()
	for id, task := range m.tasks {
		switch task.Status {
		case StatusCompleted, StatusCancelled, StatusFailed, StatusStale:
			delete(m.tasks, id)
		}
	}
	err := m.saveLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	for id, cancel := range m.cancels {
		cancel()
		if task := m.tasks[id]; task != nil && task.Status == StatusRunning {
			task.Status = StatusPaused
			task.UpdatedAt = time.Now()
		}
	}
	_ = m.saveLocked()
	m.mu.Unlock()
}

func (m *Manager) launch(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if previous := m.cancels[id]; previous != nil {
		previous()
	}
	m.cancels[id] = cancel
	m.mu.Unlock()
	go func() {
		select {
		case m.slots <- struct{}{}:
			defer func() { <-m.slots }()
		case <-ctx.Done():
			return
		}
		m.setStatus(id, StatusRunning, "")
		err := m.run(ctx, id)
		if err == nil {
			now := time.Now()
			m.mu.Lock()
			if task := m.tasks[id]; task != nil {
				task.Status = StatusCompleted
				if task.TotalBytes > 0 {
					task.DoneBytes = task.TotalBytes
				}
				task.CompletedAt = &now
				task.UpdatedAt = now
				copy := *task
				delete(m.cancels, id)
				_ = m.saveLocked()
				m.mu.Unlock()
				m.emit(copy)
				return
			}
			m.mu.Unlock()
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		status := StatusFailed
		if errors.Is(err, errSourceChanged) {
			status = StatusStale
		}
		m.setStatus(id, status, err.Error())
	}()
}

var errSourceChanged = errors.New("source file changed")

func (m *Manager) run(ctx context.Context, id string) error {
	task, err := m.task(id)
	if err != nil {
		return err
	}
	if err := m.validatePodIdentity(ctx, task.Target); err != nil {
		return fmt.Errorf("%w: %v", errSourceChanged, err)
	}
	if task.Directory {
		if task.Direction == DirectionDownload {
			return m.downloadDirectory(ctx, task)
		}
		return m.uploadDirectory(ctx, task)
	}
	if task.Direction == DirectionDownload {
		return m.download(ctx, task)
	}
	return m.upload(ctx, task)
}
