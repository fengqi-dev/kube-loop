package relayregistry

import (
	"errors"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func (registry *Registry) SetDesiredState(
	relayID string,
	state relaycontrol.State,
) error {
	if state != relaycontrol.StateReady && state != relaycontrol.StateDraining {
		return errors.New("relay desired state is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	relay := registry.relays[relayID]
	if relay == nil {
		return ErrNotFound
	}
	relay.desiredState = state
	registry.restoredDesired[relayID] = state
	return nil
}

// RestoreDesiredState installs durable control-plane intent even while a Relay
// is offline. The next registration observes it before becoming allocatable.
func (registry *Registry) RestoreDesiredState(
	relayID string,
	state relaycontrol.State,
) error {
	if strings.TrimSpace(relayID) == "" || len(relayID) > 256 ||
		(state != relaycontrol.StateReady && state != relaycontrol.StateDraining) {
		return errors.New("relay durable desired state is invalid")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.restoredDesired[relayID] = state
	if relay := registry.relays[relayID]; relay != nil {
		relay.desiredState = state
	}
	return nil
}

func (registry *Registry) UpdateControlPlaneState(
	keys relaycontrol.VerificationKeySet,
	revocations relaycontrol.RevocationSummary,
) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := keys.Validate(now); err != nil {
		return err
	}
	if err := revocations.Validate(now); err != nil {
		return err
	}
	if keys.Generation < registry.config.VerificationKeys.Generation ||
		revocations.Generation < registry.config.Revocations.Generation {
		return errors.New(
			"relay control-plane generation cannot move backwards",
		)
	}
	registry.config.VerificationKeys = cloneKeys(keys)
	registry.config.Revocations = cloneRevocations(revocations)
	return nil
}

func (registry *Registry) Snapshot() []RelayStatus {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	result := make([]RelayStatus, 0, len(registry.relays))
	for _, relay := range registry.relays {
		result = append(result, RelayStatus{
			RelayID: relay.relayID, Endpoint: relay.endpoint, State: relay.state,
			DesiredState: relay.desiredState, Capacity: relay.capacity,
			AppliedKeyGeneration:        relay.appliedKeyGeneration,
			AppliedRevocationGeneration: relay.appliedRevocationGeneration,
			LeaseExpiresAt:              relay.leaseExpiresAt, LastHeartbeatAt: relay.lastHeartbeatAt,
			Reservations: relay.reservations, Online: relay.leaseExpiresAt.After(now),
			Topology: cloneMap(relay.identity.Topology),
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
