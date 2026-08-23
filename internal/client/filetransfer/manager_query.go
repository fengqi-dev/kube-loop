package filetransfer

import (
	"slices"
	"strings"
)

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
