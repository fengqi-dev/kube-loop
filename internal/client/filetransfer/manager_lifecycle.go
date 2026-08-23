package filetransfer

import (
	"errors"
	"os"
)

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
	nextTasks := cloneTasks(manager.tasks)
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
	tasks := cloneTasks(manager.tasks)
	manager.mu.Unlock()
	return manager.persist(tasks)
}
