package relayregistry

import (
	"maps"
	"slices"
)

func (registry *Registry) Snapshot() []RelayStatus {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	result := make([]RelayStatus, 0, len(registry.relays))
	for _, relay := range registry.relays {
		result = append(result, RelayStatus{
			RelayID: relay.relayID, Endpoint: relay.endpoint, State: relay.state,
			Capacity: relay.capacity, AppliedKeyGeneration: relay.appliedKeyGeneration,
			LeaseExpiresAt: relay.leaseExpiresAt, LastHeartbeatAt: relay.lastHeartbeatAt,
			Reservations: relay.reservations, Online: relay.leaseExpiresAt.After(now),
			Topology: maps.Clone(relay.identity.Topology),
		})
	}
	slices.SortFunc(result, func(left, right RelayStatus) int {
		if left.RelayID < right.RelayID {
			return -1
		}
		if left.RelayID > right.RelayID {
			return 1
		}
		return 0
	})
	return result
}
