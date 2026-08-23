package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

func (manager *Manager) run(ctx context.Context, taskID string, entry *activeTransfer) {
	defer manager.wg.Done()
	defer close(entry.done)
	if err := manager.update(taskID, func(task *Task) { task.Status = StatusPreparing }); err != nil {
		manager.finish(
			taskID,
			filestream.TransferResult{},
			fmt.Errorf("checkpoint file transfer state: %w", err),
			false,
		)
		return
	}
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
	_ = manager.update(taskID, func(task *Task) {
		task.Status = StatusRunning
		task.DoneBytes = status.Transferred
		if status.Total != 0 {
			task.TotalBytes = status.Total
		}
	})
}

func (manager *Manager) update(taskID string, mutate func(*Task)) error {
	manager.persistMu.Lock()
	manager.mu.Lock()
	task, exists := manager.tasks[taskID]
	if !exists {
		manager.mu.Unlock()
		manager.persistMu.Unlock()
		return nil
	}
	nextTasks := cloneTasks(manager.tasks)
	manager.mu.Unlock()
	mutate(&task)
	task.UpdatedAt = manager.now().UTC()
	nextTasks[taskID] = task
	if err := manager.persist(nextTasks); err != nil {
		manager.persistMu.Unlock()
		return err
	}
	manager.mu.Lock()
	manager.tasks = nextTasks
	manager.mu.Unlock()
	manager.persistMu.Unlock()
	manager.onEvent(task)
	return nil
}

func (manager *Manager) finish(taskID string, result filestream.TransferResult, transferErr error, cancelled bool) {
	manager.persistMu.Lock()
	manager.mu.Lock()
	task, exists := manager.tasks[taskID]
	if !exists {
		manager.mu.Unlock()
		manager.persistMu.Unlock()
		return
	}
	nextTasks := cloneTasks(manager.tasks)
	manager.mu.Unlock()
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
	nextTasks[taskID] = task
	if err := manager.persist(nextTasks); err != nil {
		persistErr := fmt.Errorf("persist terminal file transfer state: %w", err)
		if task.Error != "" {
			persistErr = errors.Join(errors.New(task.Error), persistErr)
		}
		task.Status = StatusFailed
		task.Error = persistErr.Error()
		nextTasks[taskID] = task
	}
	manager.mu.Lock()
	manager.tasks = nextTasks
	delete(manager.active, taskID)
	manager.mu.Unlock()
	manager.persistMu.Unlock()
	if cleanup != "" {
		_ = os.Remove(cleanup)
	}
	manager.onEvent(task)
}
