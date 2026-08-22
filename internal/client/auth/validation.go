package auth

import (
	"crypto/rand"
	"encoding/base64"
	"strings"

	"errors"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func validateTarget(baseURL, providerID string) (string, error) {
	normalized, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	if !validProviderID(providerID) {
		return "", errors.New("authentication provider ID is invalid")
	}
	return normalized, nil
}

func validProviderID(value string) bool {
	if value == "" || len(value) > 128 {
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

func randomValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate login secret")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
