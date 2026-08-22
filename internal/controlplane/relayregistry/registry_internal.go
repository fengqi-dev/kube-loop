package relayregistry

import (
	"maps"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func (registry *Registry) registrationResponseLocked(
	relayID, selectedVersion string,
) relaycontrol.RegistrationResponse {
	relay := registry.relays[relayID]
	return relaycontrol.RegistrationResponse{
		Envelope: relaycontrol.NewRegistrationResponse().Envelope, SelectedVersion: selectedVersion,
		TicketIssuer: registry.config.TicketIssuer,
		RelayID:      relay.relayID, LeaseID: relay.leaseID, LeaseExpiresAt: relay.leaseExpiresAt,
		HeartbeatAfter: registry.config.HeartbeatAfter, DesiredState: relay.desiredState,
		Keys:        cloneKeys(registry.config.VerificationKeys),
		Revocations: cloneRevocations(registry.config.Revocations),
	}
}

func (registry *Registry) availableLocked(
	relay *relayRecord,
	now time.Time,
) bool {
	if relay.state != relaycontrol.StateReady ||
		relay.desiredState != relaycontrol.StateReady ||
		!relay.leaseExpiresAt.After(now) ||
		relay.appliedKeyGeneration < registry.config.VerificationKeys.Generation ||
		relay.appliedRevocationGeneration < registry.config.Revocations.Generation {
		return false
	}
	logical := uint64(
		relay.capacity.ActiveLogicalStreams,
	) + uint64(
		relay.reservations,
	)
	return logical < uint64(relay.capacity.MaximumLogicalStreams) &&
		relay.capacity.ActivePhysicalConnections < relay.capacity.MaximumPhysicalConnections
}

func (registry *Registry) assignmentCountLocked(relayID string) uint32 {
	var count uint32
	for _, assignment := range registry.assignments {
		if assignment.response.RelayID == relayID {
			count++
		}
	}
	return count
}

func compareTopology(left, right, wanted map[string]string) int {
	leftMatches, rightMatches := 0, 0
	for key, value := range wanted {
		if left[key] == value {
			leftMatches++
		}
		if right[key] == value {
			rightMatches++
		}
	}
	if leftMatches > rightMatches {
		return -1
	}
	if leftMatches < rightMatches {
		return 1
	}
	return 0
}

func compareRatio(leftValue, leftMaximum, rightValue, rightMaximum uint32) int {
	left := uint64(leftValue) * uint64(rightMaximum)
	right := uint64(rightValue) * uint64(leftMaximum)
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func cloneIdentity(
	identity relaycontrol.PeerIdentity,
) relaycontrol.PeerIdentity {
	identity.Topology = cloneMap(identity.Topology)
	return identity
}

func cloneKeys(
	keys relaycontrol.VerificationKeySet,
) relaycontrol.VerificationKeySet {
	copyKeys := keys
	copyKeys.Keys = append([]relaycontrol.VerificationKey(nil), keys.Keys...)
	return copyKeys
}

func cloneRevocations(
	summary relaycontrol.RevocationSummary,
) relaycontrol.RevocationSummary {
	copySummary := summary
	copySummary.Sessions = append(
		[]relaycontrol.RevokedSession(nil),
		summary.Sessions...)
	return copySummary
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	maps.Copy(result, source)
	return result
}
