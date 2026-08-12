package capability

import (
	"errors"
	"strings"
	"unicode"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
)

const (
	SchemaVersion       = 1
	MaximumCapabilities = 128
)

// Snapshot is the namespace-scoped intersection of Gateway policy and
// Kubernetes authorization returned by the Control Plane.
type Snapshot struct {
	SchemaVersion  int      `json:"schemaVersion"`
	PrincipalID    string   `json:"principalId"`
	Namespace      string   `json:"namespace"`
	GatewayVersion string   `json:"gatewayVersion"`
	Capabilities   []string `json:"capabilities"`
}

// Normalize validates an untrusted snapshot while preserving the server's
// stable capability order and removing duplicates.
func Normalize(input Snapshot) (Snapshot, error) {
	if input.SchemaVersion != SchemaVersion {
		return Snapshot{}, errors.New("unsupported capability snapshot schema")
	}
	if !validOpaque(input.PrincipalID, 512) {
		return Snapshot{}, errors.New("capability principal binding is invalid")
	}
	if input.Namespace != strings.ToLower(strings.TrimSpace(input.Namespace)) ||
		!dnsname.ValidLabel(input.Namespace) {
		return Snapshot{}, errors.New("capability namespace binding is invalid")
	}
	if !validOpaque(input.GatewayVersion, 256) {
		return Snapshot{}, errors.New("capability Gateway version binding is invalid")
	}
	if len(input.Capabilities) > MaximumCapabilities {
		return Snapshot{}, errors.New("capability snapshot contains too many entries")
	}
	seen := make(map[string]struct{}, len(input.Capabilities))
	capabilities := make([]string, 0, len(input.Capabilities))
	for _, value := range input.Capabilities {
		if !validCapability(value) {
			return Snapshot{}, errors.New("capability snapshot contains an invalid entry")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		capabilities = append(capabilities, value)
	}
	input.Capabilities = capabilities
	return input, nil
}

func validOpaque(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validCapability(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value ||
		value[0] < 'a' || value[0] > 'z' || value[len(value)-1] == '.' {
		return false
	}
	previousDot := false
	for _, character := range value {
		if character == '.' {
			if previousDot {
				return false
			}
			previousDot = true
			continue
		}
		previousDot = false
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}
