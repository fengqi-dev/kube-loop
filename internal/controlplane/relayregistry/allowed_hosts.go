package relayregistry

import (
	"errors"
	"net/url"
	"strings"
)

// ResolveAllowedHosts parses configured hosts or derives the host from publicURL.
func ResolveAllowedHosts(configured, publicURL string) ([]string, error) {
	if strings.TrimSpace(configured) != "" {
		parts := strings.Split(configured, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			if value := strings.TrimSpace(part); value != "" {
				result = append(result, value)
			}
		}
		if len(result) == 0 {
			return nil, errors.New("relay endpoint allowed hosts are empty")
		}
		return result, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("public URL is required to derive the Relay endpoint host")
	}
	return []string{parsed.Hostname()}, nil
}
