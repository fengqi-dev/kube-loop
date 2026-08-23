package podssh

import (
	"errors"
	"slices"
	"strings"
)

func (manager *Manager) Stop(profileID, endpointID string) error {
	manager.mu.Lock()
	entry := manager.active[endpointID]
	if entry != nil && entry.profile.ID == profileID {
		delete(manager.active, endpointID)
	} else {
		entry = nil
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("pod SSH endpoint is not active")
	}
	serverErr := manager.server.Disable(endpointID)
	return serverErr
}

func (manager *Manager) StopProfile(profileID string) error {
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID {
			ids = append(ids, id)
		}
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(profileID, id))
	}
	return result
}

func (manager *Manager) List(profileID string) []Info {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]Info, 0, len(manager.active))
	for _, entry := range manager.active {
		if profileID == "" || entry.profile.ID == profileID {
			item := entry.info
			item.Containers = append([]string(nil), item.Containers...)
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(left, right Info) int { return strings.Compare(left.ID, right.ID) })
	return items
}

func (manager *Manager) Shutdown() error {
	manager.lifecycle.Lock()
	defer manager.lifecycle.Unlock()
	manager.closed = true
	manager.mu.Lock()
	ids := make([]string, 0, len(manager.active))
	for id := range manager.active {
		ids = append(ids, id)
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		manager.mu.Lock()
		entry := manager.active[id]
		manager.mu.Unlock()
		if entry != nil {
			result = errors.Join(result, manager.Stop(entry.profile.ID, id))
		}
	}
	return result
}
