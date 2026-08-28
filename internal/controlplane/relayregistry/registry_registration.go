package relayregistry

import (
	"errors"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func (registry *Registry) Register(
	identity relaycontrol.PeerIdentity,
	request relaycontrol.RegistrationRequest,
) (relaycontrol.RegistrationResponse, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := identity.Validate(); err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	if err := request.Validate(now); err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	selectedVersion, err := relaycontrol.NegotiateVersion(
		registry.config.SupportedVersions,
		request.SupportedVersions,
	)
	if err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	if registry.config.EndpointPolicy != nil {
		if err := registry.config.EndpointPolicy(identity, request.Endpoint); err != nil {
			return relaycontrol.RegistrationResponse{}, err
		}
	}
	if request.AppliedKeyGeneration > registry.config.VerificationKeys.Generation ||
		request.AppliedRevocationGeneration > registry.config.Revocations.Generation {
		return relaycontrol.RegistrationResponse{}, errors.New(
			"relay reports an unknown control-plane generation",
		)
	}
	relayID, err := identity.RelayID()
	if err != nil {
		return relaycontrol.RegistrationResponse{}, err
	}
	leaseID := uuid.NewString()
	desired := relaycontrol.StateReady
	if request.State == relaycontrol.StateDraining {
		desired = relaycontrol.StateDraining
	}
	if restored, ok := registry.restoredDesired[relayID]; ok {
		desired = restored
	}
	reservations := registry.assignmentCountLocked(relayID)
	registry.relays[relayID] = &relayRecord{
		identity: cloneIdentity(identity), relayID: relayID, leaseID: leaseID,
		endpoint: request.Endpoint, state: request.State, desiredState: desired,
		capacity: request.Capacity, appliedKeyGeneration: request.AppliedKeyGeneration,
		appliedRevocationGeneration: request.AppliedRevocationGeneration,
		leaseExpiresAt: now.Add(
			registry.config.LeaseDuration,
		), lastHeartbeatAt: now,
		reservations: reservations, selectedVersion: selectedVersion,
	}
	return registry.registrationResponseLocked(relayID, selectedVersion), nil
}

func (registry *Registry) Heartbeat(
	identity relaycontrol.PeerIdentity,
	request relaycontrol.HeartbeatRequest,
) (relaycontrol.HeartbeatResponse, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.config.Now().UTC()
	if err := identity.Validate(); err != nil {
		return relaycontrol.HeartbeatResponse{}, err
	}
	if err := request.Validate(now); err != nil {
		return relaycontrol.HeartbeatResponse{}, err
	}
	relayID, err := identity.RelayID()
	if err != nil {
		return relaycontrol.HeartbeatResponse{}, err
	}
	relay := registry.relays[relayID]
	if relay == nil {
		return relaycontrol.HeartbeatResponse{}, ErrNotFound
	}
	if relay.leaseID != request.LeaseID {
		return relaycontrol.HeartbeatResponse{}, ErrConflict
	}
	if relay.selectedVersion != request.APIVersion {
		return relaycontrol.HeartbeatResponse{}, ErrConflict
	}
	if !relay.leaseExpiresAt.After(now) {
		return relaycontrol.HeartbeatResponse{}, ErrNotFound
	}
	if request.AppliedKeyGeneration > registry.config.VerificationKeys.Generation ||
		request.AppliedRevocationGeneration > registry.config.Revocations.Generation {
		return relaycontrol.HeartbeatResponse{}, errors.New(
			"relay reports an unknown control-plane generation",
		)
	}
	relay.state = request.State
	relay.capacity = request.Capacity
	relay.appliedKeyGeneration = request.AppliedKeyGeneration
	relay.appliedRevocationGeneration = request.AppliedRevocationGeneration
	relay.lastHeartbeatAt = now
	relay.leaseExpiresAt = now.Add(registry.config.LeaseDuration)
	relay.trafficEncryption = request.TrafficEncryption != nil && *request.TrafficEncryption
	relay.noisePublicKey = request.NoisePublicKey
	return relaycontrol.HeartbeatResponse{
		Envelope: relaycontrol.NewHeartbeatResponseForVersion(
			relay.selectedVersion,
		).Envelope,
		LeaseExpiresAt: relay.leaseExpiresAt, HeartbeatAfter: registry.config.HeartbeatAfter,
		DesiredState: relay.desiredState, Keys: cloneKeys(registry.config.VerificationKeys),
		Revocations: cloneRevocations(registry.config.Revocations),
	}, nil
}
