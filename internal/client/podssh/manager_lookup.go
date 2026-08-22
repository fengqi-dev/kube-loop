package podssh

import (
	"errors"
	"slices"
	"strings"

	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func (manager *Manager) lookup(target localpodssh.Target) (profile.Profile, remote.Session, error) {
	manager.mu.Lock()
	entry := manager.findLocked(target.Context, target.Namespace, target.Pod)
	if entry == nil || !slices.Contains(entry.target.Containers, target.Container) {
		manager.mu.Unlock()
		return profile.Profile{}, remote.Session{}, errors.New("pod SSH endpoint is no longer active")
	}
	serverProfile := entry.profile
	expectedSession := entry.session
	manager.mu.Unlock()

	current, err := manager.sessions.Current(serverProfile.ID)
	if err != nil {
		return profile.Profile{}, remote.Session{}, err
	}
	if current.ID != expectedSession.ID || current.Namespace != target.Namespace ||
		current.State != podSSHSessionActive {
		return profile.Profile{}, remote.Session{}, errors.New("pod SSH Session changed")
	}
	return serverProfile, current, nil
}

func (manager *Manager) findLocked(profileID, namespace, pod string) *activeEndpoint {
	for _, entry := range manager.active {
		if entry.profile.ID == profileID && entry.info.Namespace == namespace && entry.info.Pod == pod {
			return entry
		}
	}
	return nil
}

func normalizeContainers(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
