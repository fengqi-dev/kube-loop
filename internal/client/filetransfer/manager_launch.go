package filetransfer

import "context"

func (manager *Manager) launch(ctx context.Context, task Task, entry *activeTransfer) error {
	manager.persistMu.Lock()
	manager.mu.Lock()
	nextTasks := cloneTasks(manager.tasks)
	manager.mu.Unlock()
	return manager.launchPrepared(ctx, task, entry, nextTasks)
}

// launchPrepared commits a Task while the caller owns persistMu. Resume uses
// it to keep resumability validation and activation in one transaction.
func (manager *Manager) launchPrepared(
	ctx context.Context,
	task Task,
	entry *activeTransfer,
	nextTasks map[string]Task,
) error {
	nextTasks[task.ID] = task
	if err := manager.persist(nextTasks); err != nil {
		manager.persistMu.Unlock()
		entry.cancel()
		return err
	}
	manager.mu.Lock()
	manager.tasks = nextTasks
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	manager.persistMu.Unlock()
	manager.onEvent(task)
	manager.wg.Go(func() {
		manager.run(ctx, task.ID, entry)
	})
	return nil
}
