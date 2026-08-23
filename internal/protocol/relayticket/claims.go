package relayticket

import (
	"errors"
	"slices"
	"strings"
	"time"
)

func validateClaims(claims Claims) error {
	if claims.Version != Version || !validText(claims.Issuer, 512) || !validIdentifier(claims.Audience, 128) ||
		!validIdentifier(claims.IdentityID, 256) || !validIdentifier(claims.DeviceID, 256) ||
		!validIdentifier(claims.SessionID, 128) || !validIdentifier(claims.Namespace, 253) ||
		claims.SessionGeneration == 0 || !validIdentifier(claims.TicketID, 128) ||
		len(claims.Groups) > 128 || len(claims.Operations) == 0 || len(claims.Operations) > 16 {
		return errors.New("relay ticket claims are invalid")
	}
	groups := make(map[string]struct{}, len(claims.Groups))
	for _, group := range claims.Groups {
		if !validText(group, 256) {
			return errors.New("relay ticket group is invalid")
		}
		if _, exists := groups[group]; exists {
			return errors.New("relay ticket group is duplicated")
		}
		groups[group] = struct{}{}
	}
	seen := make(map[string]struct{}, len(claims.Operations))
	for _, operation := range claims.Operations {
		if !validIdentifier(operation, 64) {
			return errors.New("relay ticket operation is invalid")
		}
		if _, exists := seen[operation]; exists {
			return errors.New("relay ticket operation is duplicated")
		}
		seen[operation] = struct{}{}
	}
	if !validLowerHex(claims.NetworkSpecHash, 64) {
		return errors.New("relay ticket network spec hash is invalid")
	}
	if claims.IssuedAt <= 0 || claims.NotBefore <= 0 || claims.ExpiresAt <= claims.IssuedAt ||
		claims.NotBefore < claims.IssuedAt || time.Duration(claims.ExpiresAt-claims.IssuedAt)*time.Second > MaximumLifetime {
		return errors.New("relay ticket lifetime is invalid")
	}
	return nil
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !containsControl(value)
}

func validIdentifier(value string, maximum int) bool {
	if !validText(value, maximum) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._:@/", character) {
			continue
		}
		return false
	}
	return true
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	return slices.Contains(values, target)
}
