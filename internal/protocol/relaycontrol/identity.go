package relaycontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// PeerIdentity is populated only by the authenticated internal transport
// (mTLS/SPIFFE or a verified projected ServiceAccount token). It is never
// decoded from a registration body.
type PeerIdentity struct {
	TrustDomain    string
	Namespace      string
	ServiceAccount string
	PodUID         string
	// Topology is populated from trusted Pod/Node identity lookup, never from
	// the registration JSON body.
	Topology map[string]string
}

func (identity PeerIdentity) Validate() error {
	values := []struct {
		name  string
		value string
		max   int
	}{
		{"trust domain", identity.TrustDomain, 253},
		{"namespace", identity.Namespace, 63},
		{"service account", identity.ServiceAccount, 63},
		{"Pod UID", identity.PodUID, 128},
	}
	for _, item := range values {
		if !safeIdentityValue(item.value, item.max) {
			return errors.New("relay peer " + item.name + " is invalid")
		}
	}
	if !validTopology(identity.Topology) {
		return errors.New("relay peer topology is invalid")
	}
	return nil
}

// RelayID derives a stable, non-forgeable registry key from authenticated peer
// identity. The raw Pod identity is not exposed as the public Relay ID.
func (identity PeerIdentity) RelayID() (string, error) {
	if err := identity.Validate(); err != nil {
		return "", err
	}
	canonical := strings.Join([]string{
		strings.ToLower(identity.TrustDomain), identity.Namespace,
		identity.ServiceAccount, identity.PodUID,
	}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return "relay-" + hex.EncodeToString(digest[:]), nil
}

func safeIdentityValue(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("-._:/", character) {
			continue
		}
		return false
	}
	return true
}

func validTopology(topology map[string]string) bool {
	if len(topology) > 16 {
		return false
	}
	for key, value := range topology {
		if !safeIdentityValue(key, 128) || !safeIdentityValue(value, 128) {
			return false
		}
	}
	return true
}
