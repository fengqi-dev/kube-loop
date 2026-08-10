package relaycontrol

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	minimumHeartbeat = time.Second
	maximumHeartbeat = time.Minute
	maximumLease     = 5 * time.Minute
)

func (request RegistrationRequest) Validate(now time.Time) error {
	if err := request.Envelope.validate(KindRegistrationRequest); err != nil {
		return err
	}
	if !slices.Contains(request.SupportedVersions, request.APIVersion) ||
		!validVersionList(request.SupportedVersions) {
		return errors.New("relay supported protocol versions are invalid")
	}
	if err := validateEndpoint(request.Endpoint); err != nil {
		return err
	}
	if request.State != StateReady && request.State != StateDraining {
		return errors.New("relay registration state is invalid")
	}
	return request.Capacity.validate()
}

func (response RegistrationResponse) Validate(now time.Time) error {
	if err := response.Envelope.validate(KindRegistrationResponse); err != nil {
		return err
	}
	if response.SelectedVersion != response.APIVersion {
		return errors.New("relay selected protocol version is invalid")
	}
	if !validRelayID(response.RelayID) {
		return errors.New("relay registration response ID is invalid")
	}
	if err := validateLease(response.LeaseID, response.LeaseExpiresAt, response.HeartbeatAfter, now); err != nil {
		return err
	}
	if response.DesiredState != StateReady && response.DesiredState != StateDraining {
		return errors.New("relay registration desired state is invalid")
	}
	if err := response.Keys.validate(now); err != nil {
		return err
	}
	return response.Revocations.validate(now)
}

func (request HeartbeatRequest) Validate(time.Time) error {
	if err := request.Envelope.validate(KindHeartbeatRequest); err != nil {
		return err
	}
	if _, err := uuid.Parse(request.LeaseID); err != nil {
		return errors.New("relay heartbeat lease ID is invalid")
	}
	if request.State != StateReady && request.State != StateDraining {
		return errors.New("relay heartbeat state is invalid")
	}
	return request.Capacity.validate()
}

func (response HeartbeatResponse) Validate(now time.Time) error {
	if err := response.Envelope.validate(KindHeartbeatResponse); err != nil {
		return err
	}
	if err := validateLeaseTiming(response.LeaseExpiresAt, response.HeartbeatAfter, now); err != nil {
		return err
	}
	if response.DesiredState != StateReady && response.DesiredState != StateDraining {
		return errors.New("relay heartbeat desired state is invalid")
	}
	if err := response.Keys.validate(now); err != nil {
		return err
	}
	return response.Revocations.validate(now)
}

func (request AllocationRequest) Validate(time.Time) error {
	if err := request.Envelope.validate(KindAllocationRequest); err != nil {
		return err
	}
	if _, err := uuid.Parse(request.SessionID); err != nil || request.Generation == 0 {
		return errors.New("Session allocation identity is invalid")
	}
	if len(request.NetworkSpecHash) != sha256HexLength {
		return errors.New("Session allocation NetworkSpec hash is invalid")
	}
	if _, err := hex.DecodeString(request.NetworkSpecHash); err != nil {
		return errors.New("Session allocation NetworkSpec hash is invalid")
	}
	if !validTopology(request.Topology) {
		return errors.New("Session allocation topology is invalid")
	}
	return nil
}

func (response AllocationResponse) Validate(now time.Time) error {
	if err := response.Envelope.validate(KindAllocationResponse); err != nil {
		return err
	}
	if !validRelayID(response.RelayID) {
		return errors.New("Session assignment Relay ID is invalid")
	}
	if _, err := uuid.Parse(response.LeaseID); err != nil {
		return errors.New("Session assignment lease ID is invalid")
	}
	if err := validateEndpoint(response.Endpoint); err != nil {
		return err
	}
	if response.AssignedAt.IsZero() || response.AssignedAt.After(now.Add(time.Minute)) {
		return errors.New("Session assignment time is invalid")
	}
	return nil
}

func (envelope Envelope) validate(kind string) error {
	if envelope.APIVersion != APIVersion {
		return fmt.Errorf("unsupported relay control API version %q", envelope.APIVersion)
	}
	if envelope.Kind != kind {
		return fmt.Errorf("relay control kind must be %q", kind)
	}
	return nil
}

func (capacity Capacity) validate() error {
	if capacity.MaximumPhysicalConnections == 0 || capacity.MaximumPhysicalConnections > 1<<20 ||
		capacity.MaximumLogicalStreams == 0 || capacity.MaximumLogicalStreams > 1<<24 ||
		capacity.ActivePhysicalConnections > capacity.MaximumPhysicalConnections ||
		capacity.ActiveLogicalStreams > capacity.MaximumLogicalStreams {
		return errors.New("relay capacity is invalid")
	}
	return nil
}

func (keys VerificationKeySet) validate(now time.Time) error {
	if keys.Generation == 0 || len(keys.Keys) == 0 || len(keys.Keys) > 8 {
		return errors.New("RelayTicket verification key set is invalid")
	}
	seen := make(map[string]struct{}, len(keys.Keys))
	usable := false
	for _, key := range keys.Keys {
		if !safeIdentityValue(key.ID, 128) || key.Algorithm != "EdDSA" ||
			key.NotBefore.IsZero() || key.NotAfter.IsZero() || !key.NotAfter.After(key.NotBefore) {
			return errors.New("RelayTicket verification key is invalid")
		}
		if _, exists := seen[key.ID]; exists {
			return errors.New("RelayTicket verification key ID is duplicated")
		}
		seen[key.ID] = struct{}{}
		block, rest := pem.Decode([]byte(key.PublicKey))
		if block == nil || block.Type != "PUBLIC KEY" || len(rest) != 0 {
			return errors.New("RelayTicket verification public key is invalid")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		publicKey, ok := parsed.(ed25519.PublicKey)
		if err != nil || !ok || len(publicKey) != ed25519.PublicKeySize {
			return errors.New("RelayTicket verification public key is invalid")
		}
		if !now.Before(key.NotBefore) && now.Before(key.NotAfter) {
			usable = true
		}
	}
	if !usable {
		return errors.New("RelayTicket verification key set has no currently usable key")
	}
	return nil
}

func (keys VerificationKeySet) Validate(now time.Time) error {
	return keys.validate(now)
}

const sha256HexLength = 64

func (summary RevocationSummary) validate(now time.Time) error {
	return summary.Validate(now)
}

func validateLease(id string, expiresAt time.Time, heartbeat time.Duration, now time.Time) error {
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("relay lease is invalid")
	}
	return validateLeaseTiming(expiresAt, heartbeat, now)
}

func validateLeaseTiming(expiresAt time.Time, heartbeat time.Duration, now time.Time) error {
	if expiresAt.IsZero() || !expiresAt.After(now) ||
		expiresAt.After(now.Add(maximumLease)) || heartbeat < minimumHeartbeat || heartbeat > maximumHeartbeat ||
		heartbeat >= expiresAt.Sub(now) {
		return errors.New("relay lease is invalid")
	}
	return nil
}

func validateEndpoint(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" ||
		strings.TrimRight(parsed.Path, "/") != parsed.Path {
		return errors.New("relay endpoint must be an absolute WSS URL with a fixed path")
	}
	return nil
}

func validRelayID(value string) bool {
	if !strings.HasPrefix(value, "relay-") || len(value) != len("relay-")+sha256HexLength {
		return false
	}
	_, err := hex.DecodeString(value[len("relay-"):])
	return err == nil
}
