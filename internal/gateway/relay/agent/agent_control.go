package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func (agent *Agent) register(ctx context.Context) error {
	state, capacity := agent.config.Reporter.Snapshot()
	keyGeneration, revocationGeneration := agent.config.Applier.AppliedGenerations()
	request := relaycontrol.NewRegistrationRequestWithNegotiation()
	request.Endpoint = agent.config.Endpoint
	request.State = state
	request.Capacity = capacity
	request.AppliedKeyGeneration = keyGeneration
	request.AppliedRevocationGeneration = revocationGeneration
	var response relaycontrol.RegistrationResponse
	if err := call(
		ctx, agent, http.MethodPost, "/internal/v1/relays/register",
		request, relaycontrol.DecodeRegistrationResponse, &response,
	); err != nil {
		return err
	}
	if err := agent.config.Applier.Apply(
		response.TicketIssuer, response.RelayID, response.Keys, response.Revocations,
	); err != nil {
		return fmt.Errorf("apply Relay registration control state: %w", err)
	}
	if response.DesiredState == relaycontrol.StateDraining {
		agent.config.Reporter.BeginDrain()
	}
	if agent.config.TrafficEncryption && response.SelectedVersion != relaycontrol.APIVersionV2 {
		return errors.New("control plane does not support encrypted Relay traffic")
	}
	agent.mu.Lock()
	agent.relayID = response.RelayID
	agent.ticketIssuer = response.TicketIssuer
	agent.leaseID = response.LeaseID
	agent.leaseExpiresAt = response.LeaseExpiresAt
	agent.heartbeatAfter = response.HeartbeatAfter
	agent.selectedVersion = response.SelectedVersion
	agent.lastError = nil
	agent.mu.Unlock()
	// Acknowledge the just-applied generations immediately. Registration
	// reports the previous generations and is intentionally not allocatable yet.
	return agent.heartbeat(ctx)
}

func (agent *Agent) heartbeat(ctx context.Context) error {
	agent.mu.RLock()
	leaseID := agent.leaseID
	selectedVersion := agent.selectedVersion
	agent.mu.RUnlock()
	if leaseID == "" {
		return errors.New("relay agent has no lease")
	}
	state, capacity := agent.config.Reporter.Snapshot()
	keyGeneration, revocationGeneration := agent.config.Applier.AppliedGenerations()
	request := relaycontrol.NewHeartbeatRequestForVersion(selectedVersion)
	request.LeaseID = leaseID
	request.State = state
	request.Capacity = capacity
	request.AppliedKeyGeneration = keyGeneration
	request.AppliedRevocationGeneration = revocationGeneration
	if selectedVersion == relaycontrol.APIVersionV2 {
		request.TrafficEncryption = new(agent.config.TrafficEncryption)
		if agent.config.TrafficEncryption {
			request.NoisePublicKey = agent.config.NoisePublicKey
		}
	}
	var response relaycontrol.HeartbeatResponse
	if err := call(
		ctx, agent, http.MethodPut, "/internal/v1/relays/heartbeat",
		request, relaycontrol.DecodeHeartbeatResponse, &response,
	); err != nil {
		return err
	}
	agent.mu.RLock()
	relayID := agent.relayID
	ticketIssuer := agent.ticketIssuer
	agent.mu.RUnlock()
	if err := agent.config.Applier.Apply(ticketIssuer, relayID, response.Keys, response.Revocations); err != nil {
		return fmt.Errorf("apply Relay heartbeat control state: %w", err)
	}
	if response.DesiredState == relaycontrol.StateDraining {
		agent.config.Reporter.BeginDrain()
	}
	agent.mu.Lock()
	agent.leaseExpiresAt = response.LeaseExpiresAt
	agent.heartbeatAfter = response.HeartbeatAfter
	agent.lastError = nil
	agent.mu.Unlock()
	return nil
}
