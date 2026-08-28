package relayregistry

import (
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func (registry *Registry) Allocate(
	request relaycontrol.AllocationRequest,
) (relaycontrol.AllocationResponse, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := request.Validate(now); err != nil {
		return relaycontrol.AllocationResponse{}, err
	}
	wantedEncryption := request.TrafficEncryption != nil && *request.TrafficEncryption
	var displaced assignmentRecord
	var displacedRelay *relayRecord
	reservationReleased := false
	reassigning := false
	if existing, ok := registry.assignments[request.SessionID]; ok {
		if request.Generation < existing.generation ||
			(request.Generation == existing.generation && request.NetworkSpecHash != existing.networkSpecHash) {
			return relaycontrol.AllocationResponse{}, ErrConflict
		}
		relay := registry.relays[existing.response.RelayID]
		if relay == nil || relay.leaseID != existing.response.LeaseID ||
			!registry.availableLocked(relay, now) ||
			!relaySupportsEncryption(relay, wantedEncryption) {
			// A newer authoritative Session generation is the fencing boundary that
			// permits failover. Reusing the same generation would allow two Relays
			// to accept tickets for one Session concurrently during a partition.
			if request.Generation == existing.generation {
				return relaycontrol.AllocationResponse{}, ErrAssignedRelayUnavailable
			}
			displaced = existing
			displacedRelay = relay
			reassigning = true
			delete(registry.assignments, request.SessionID)
			if relay != nil && relay.reservations > 0 {
				relay.reservations--
				reservationReleased = true
			}
		} else {
			existing.generation = request.Generation
			existing.networkSpecHash = request.NetworkSpecHash
			registry.assignments[request.SessionID] = existing
			return existing.response, nil
		}
	}
	candidates := make([]*relayRecord, 0, len(registry.relays))
	for _, relay := range registry.relays {
		if registry.availableLocked(relay, now) &&
			relaySupportsEncryption(relay, wantedEncryption) {
			candidates = append(candidates, relay)
		}
	}
	if len(candidates) == 0 {
		if reassigning {
			registry.assignments[request.SessionID] = displaced
			if reservationReleased {
				displacedRelay.reservations++
			}
			return relaycontrol.AllocationResponse{}, ErrAssignedRelayUnavailable
		}
		return relaycontrol.AllocationResponse{}, ErrUnavailable
	}
	slices.SortFunc(candidates, func(left, right *relayRecord) int {
		if comparison := compareTopology(
			left.identity.Topology,
			right.identity.Topology,
			request.Topology,
		); comparison != 0 {
			return comparison
		}
		if comparison := compareRatio(
			left.capacity.ActiveLogicalStreams+left.reservations, left.capacity.MaximumLogicalStreams,
			right.capacity.ActiveLogicalStreams+right.reservations, right.capacity.MaximumLogicalStreams,
		); comparison != 0 {
			return comparison
		}
		if comparison := compareRatio(
			left.capacity.ActivePhysicalConnections, left.capacity.MaximumPhysicalConnections,
			right.capacity.ActivePhysicalConnections, right.capacity.MaximumPhysicalConnections,
		); comparison != 0 {
			return comparison
		}
		if left.relayID < right.relayID {
			return -1
		}
		if left.relayID > right.relayID {
			return 1
		}
		return 0
	})
	selected := candidates[0]
	selected.reservations++
	assignment := relaycontrol.AllocationResponse{
		Envelope: relaycontrol.NewAllocationResponse().Envelope,
		RelayID:  selected.relayID, LeaseID: selected.leaseID,
		Endpoint: selected.endpoint, AssignedAt: now,
		TrafficEncryption: selected.trafficEncryption,
		NoisePublicKey:    selected.noisePublicKey,
	}
	registry.assignments[request.SessionID] = assignmentRecord{
		response: assignment, generation: request.Generation, networkSpecHash: request.NetworkSpecHash,
	}
	return assignment, nil
}

func relaySupportsEncryption(relay *relayRecord, wanted bool) bool {
	if relay.trafficEncryption != wanted {
		return false
	}
	if !wanted {
		return true
	}
	return relay.selectedVersion == relaycontrol.APIVersionV2 && relay.noisePublicKey != ""
}

func (registry *Registry) Release(sessionID string, generation uint64) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	assignment, exists := registry.assignments[sessionID]
	if !exists || assignment.generation != generation {
		return false
	}
	delete(registry.assignments, sessionID)
	if relay := registry.relays[assignment.response.RelayID]; relay != nil &&
		relay.reservations > 0 {
		relay.reservations--
	}
	return true
}
