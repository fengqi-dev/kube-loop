package filetransfer

import (
	"context"
	"errors"
	"os"

	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
)

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
