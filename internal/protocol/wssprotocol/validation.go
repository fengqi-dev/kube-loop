package wssprotocol

import (
	"errors"
	"slices"
	"strings"
	"time"

	version "github.com/hashicorp/go-version"
)

func (hello ClientHello) Validate() error {
	if hello.Type != KindClientHello || len(hello.ProtocolVersions) == 0 || len(hello.ProtocolVersions) > 8 ||
		!safeText(hello.ClientVersion, 64) || !safeIdentifier(hello.DeviceID, 256) ||
		!validUniqueIdentifiers(hello.Capabilities, 32, 64) {
		return ErrInvalidHandshake
	}
	if !validUniqueIdentifiers(hello.ProtocolVersions, 8, 16) {
		return ErrInvalidHandshake
	}
	return nil
}

func (hello ServerHello) Validate() error {
	validConnectionLimits := hello.Limits.MaximumConnectionsPerUser >= 1 &&
		hello.Limits.MaximumConnectionsPerUser <= hello.Limits.MaximumPhysicalConnections
	if hello.Type != KindServerHello || !safeIdentifier(hello.ProtocolVersion, 16) ||
		!safeText(hello.ServerVersion, 64) || !validUniqueIdentifiers(hello.Capabilities, 32, 64) ||
		hello.Limits.MaximumFrameBytes < MaximumHandshakeBytes || hello.Limits.MaximumFrameBytes > 16<<20 ||
		hello.Limits.MaximumStreamFrameBytes < 1 || hello.Limits.MaximumStreamFrameBytes > 1<<20 ||
		hello.Limits.MaximumStreamsPerConnection < 1 || hello.Limits.MaximumStreamsPerConnection > 1<<20 ||
		hello.Limits.MaximumPhysicalConnections < 1 || hello.Limits.MaximumPhysicalConnections > 1<<20 ||
		!validConnectionLimits ||
		hello.Limits.StreamIdleTimeoutMillis <= 0 || hello.Limits.StreamIdleTimeoutMillis > (24*time.Hour).Milliseconds() {
		return ErrInvalidHandshake
	}
	return nil
}

func (reject Reject) Validate() error {
	if reject.Type != KindReject || !safeIdentifier(reject.Code, 64) || !safeText(reject.Message, 256) ||
		(len(reject.SupportedVersions) > 0 && !validUniqueIdentifiers(reject.SupportedVersions, 8, 16)) {
		return ErrInvalidHandshake
	}
	return nil
}

func Negotiate(client, server []string) (string, error) {
	if !validUniqueIdentifiers(client, 8, 16) || !validUniqueIdentifiers(server, 8, 16) {
		return "", ErrInvalidHandshake
	}
	for _, supported := range server {
		if slices.Contains(client, supported) {
			return supported, nil
		}
	}
	return "", errors.New(CodeVersionMismatch)
}

func CheckClientVersion(current, minimum string) error {
	current = strings.TrimSpace(current)
	minimum = strings.TrimSpace(minimum)
	if minimum == "" || current == "dev" {
		return nil
	}
	currentVersion, currentErr := version.NewVersion(strings.TrimPrefix(current, "v"))
	minimumVersion, minimumErr := version.NewVersion(strings.TrimPrefix(minimum, "v"))
	if currentErr != nil || minimumErr != nil || currentVersion.LessThan(minimumVersion) {
		return errors.New(CodeClientVersionUnsupported)
	}
	return nil
}

func validUniqueIdentifiers(values []string, maximumItems, maximumLength int) bool {
	if len(values) == 0 || len(values) > maximumItems {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !safeIdentifier(value, maximumLength) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func safeText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func safeIdentifier(value string, maximum int) bool {
	if !safeText(value, maximum) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}
