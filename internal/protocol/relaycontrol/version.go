package relaycontrol

import (
	"errors"
	"slices"
	"strings"
)

// NegotiateVersion returns the first local preference supported by the peer.
// Registration itself always uses the oldest mutually supported bootstrap
// envelope; a future version switches only after this response is accepted.
func NegotiateVersion(localPreference, peerSupported []string) (string, error) {
	if !validVersionList(localPreference) || !validVersionList(peerSupported) {
		return "", errors.New("relay protocol version list is invalid")
	}
	for _, version := range localPreference {
		if slices.Contains(peerSupported, version) {
			return version, nil
		}
	}
	return "", errors.New("relay protocol versions are incompatible")
}

func validVersionList(versions []string) bool {
	if len(versions) == 0 || len(versions) > 8 {
		return false
	}
	seen := make(map[string]struct{}, len(versions))
	for _, version := range versions {
		if version == "" || len(version) > 64 || strings.TrimSpace(version) != version ||
			!strings.HasPrefix(version, "relay.kubeloop.io/v") {
			return false
		}
		if _, exists := seen[version]; exists {
			return false
		}
		seen[version] = struct{}{}
	}
	return true
}
