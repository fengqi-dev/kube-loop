package discovery

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	version "github.com/hashicorp/go-version"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) validate(baseURL string, document Document) error {
	if !validIdentifier(document.ServiceID) {
		return errors.New("service discovery returned an invalid service ID")
	}
	publicURL, err := profile.NormalizeBaseURL(document.PublicURL)
	if err != nil {
		return errors.New("service discovery returned an invalid public URL")
	}
	if !sameOrigin(baseURL, publicURL) {
		return errors.New("service discovery public URL does not match the configured origin")
	}
	if _, err := NormalizeTunnelPath(document.TunnelPath); err != nil {
		return fmt.Errorf("service discovery returned an invalid tunnel path: %w", err)
	}
	if !slices.Contains(document.APIVersions, "v2") {
		return errors.New("service does not support API v2")
	}
	if err := validateProtocol(client.protocolVersion, document.ProtocolMin, document.ProtocolMax); err != nil {
		return err
	}
	if err := validateClientVersion(client.clientVersion, document.MinClientVersion); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(document.AuthMethods))
	for _, method := range document.AuthMethods {
		method.ID = strings.TrimSpace(method.ID)
		if !validIdentifier(method.ID) {
			return errors.New("service discovery returned an invalid authentication method ID")
		}
		if _, exists := seen[method.ID]; exists {
			return errors.New("service discovery returned duplicate authentication method IDs")
		}
		seen[method.ID] = struct{}{}
		switch {
		case method.Type == discoveryAuthOIDC && method.Interaction == discoveryAuthBrowser:
		case method.Type == discoveryCallbackLocal && method.Interaction == discoveryAuthBrowser:
		default:
			return fmt.Errorf("service discovery returned unsupported authentication method %q", method.ID)
		}
	}
	return nil
}

func NormalizeTunnelPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = DefaultTunnelPath
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(value, "/") || parsed.IsAbs() || parsed.Host != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.EscapedPath() != value ||
		strings.Contains(value, "//") || strings.Contains(value, "/./") || strings.Contains(value, "/../") ||
		strings.HasSuffix(value, "/.") || strings.HasSuffix(value, "/..") {
		return "", errors.New("must be a clean absolute URL path without escaping, query parameters or a fragment")
	}
	return value, nil
}

func validateProtocol(current, minimum, maximum string) error {
	currentVersion, currentErr := version.NewVersion(strings.TrimSpace(current))
	minimumVersion, minimumErr := version.NewVersion(strings.TrimSpace(minimum))
	maximumVersion, maximumErr := version.NewVersion(strings.TrimSpace(maximum))
	if currentErr != nil || minimumErr != nil || maximumErr != nil || minimumVersion.GreaterThan(maximumVersion) {
		return errors.New("service discovery returned an invalid protocol range")
	}
	if currentVersion.LessThan(minimumVersion) || currentVersion.GreaterThan(maximumVersion) {
		message := fmt.Sprintf(
			"service protocol range %s-%s is incompatible with client protocol %s",
			minimum,
			maximum,
			current,
		)
		return &CompatibilityError{
			Code: CodeVersionMismatch, Message: message, ProtocolVersion: current,
			Minimum: minimum, Maximum: maximum,
		}
	}
	return nil
}

func validateClientVersion(current, minimum string) error {
	current = strings.TrimSpace(current)
	minimum = strings.TrimSpace(minimum)
	if minimum == "" || current == "" || current == "dev" {
		return nil
	}
	currentVersion, currentErr := version.NewVersion(strings.TrimPrefix(current, "v"))
	minimumVersion, minimumErr := version.NewVersion(strings.TrimPrefix(minimum, "v"))
	if minimumErr != nil {
		return errors.New("service discovery returned an invalid minimum client version")
	}
	if currentErr != nil {
		return errors.New("client version is invalid")
	}
	if currentVersion.LessThan(minimumVersion) {
		return &CompatibilityError{
			Code:          CodeClientVersionUnsupported,
			Message:       fmt.Sprintf("service requires client version %s or newer", minimum),
			ClientVersion: current, Minimum: minimum,
		}
	}
	return nil
}

func sameOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Hostname(), rightURL.Hostname())
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
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
