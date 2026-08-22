package profile

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
)

const currentVersion = 1

func normalizeState(state State) (State, error) {
	if state.Version != currentVersion {
		return State{}, fmt.Errorf("unsupported Server Profile store version %d", state.Version)
	}
	seen := make(map[string]struct{}, len(state.Profiles))
	for index, item := range state.Profiles {
		normalized, err := normalizeProfile(item)
		if err != nil {
			return State{}, fmt.Errorf("server Profile %d: %w", index, err)
		}
		if _, exists := seen[normalized.ID]; exists {
			return State{}, fmt.Errorf("duplicate Server Profile ID %q", normalized.ID)
		}
		seen[normalized.ID] = struct{}{}
		state.Profiles[index] = normalized
	}
	if state.Profiles == nil {
		state.Profiles = []Profile{}
	}
	if state.ActiveProfileID != "" {
		if _, exists := seen[state.ActiveProfileID]; !exists {
			return State{}, errors.New("active Server Profile does not exist")
		}
	}
	return state, nil
}

func normalizeProfile(profile Profile) (Profile, error) {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.LastIdentityID = strings.TrimSpace(profile.LastIdentityID)
	profile.LastUserName = strings.TrimSpace(profile.LastUserName)
	profile.LastNamespace = strings.TrimSpace(profile.LastNamespace)
	profile.DNSNamespace = strings.TrimSpace(profile.DNSNamespace)
	if profile.SOCKSPort < 0 || profile.SOCKSPort > 65535 {
		return Profile{}, errors.New("server Profile SOCKS port must be between 1 and 65535")
	}
	if profile.DNSNamespace != "" && !dnsname.ValidLabel(profile.DNSNamespace) {
		return Profile{}, errors.New("server Profile DNS namespace is invalid")
	}
	aliases, err := normalizeHostAliases(profile.HostAliases)
	if err != nil {
		return Profile{}, err
	}
	profile.HostAliases = aliases
	if profile.ID == "" || len(profile.ID) > 128 {
		return Profile{}, errors.New("server Profile ID must contain 1-128 characters")
	}
	baseURL, err := NormalizeBaseURL(profile.BaseURL)
	if err != nil {
		return Profile{}, err
	}
	profile.BaseURL = baseURL
	profile.TunnelPath = strings.TrimSpace(profile.TunnelPath)
	if profile.TunnelPath == "" {
		profile.TunnelPath = "/tunnel"
	}
	parsedTunnelPath, err := url.ParseRequestURI(profile.TunnelPath)
	if err != nil {
		return Profile{}, errors.New("server Profile tunnel path is invalid")
	}
	invalidLocation := !strings.HasPrefix(profile.TunnelPath, "/") || parsedTunnelPath.IsAbs()
	invalidMetadata := parsedTunnelPath.Host != "" || parsedTunnelPath.RawQuery != "" || parsedTunnelPath.Fragment != ""
	nonCanonical := parsedTunnelPath.EscapedPath() != profile.TunnelPath || strings.Contains(profile.TunnelPath, "//")
	containsDotPath := strings.Contains(profile.TunnelPath, "/./") || strings.Contains(profile.TunnelPath, "/../")
	endsWithDotPath := strings.HasSuffix(profile.TunnelPath, "/.") || strings.HasSuffix(profile.TunnelPath, "/..")
	if invalidLocation || invalidMetadata || nonCanonical || containsDotPath || endsWithDotPath {
		return Profile{}, errors.New("server Profile tunnel path is invalid")
	}
	return profile, nil
}

func NormalizeBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return "", errors.New("service address must be an absolute URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("service address must use HTTP or HTTPS")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("service address must not contain credentials, query or fragment")
	}
	if parsed.Path != "" || parsed.RawPath != "" {
		return "", errors.New("service address must be an origin without a path")
	}
	return parsed.String(), nil
}

func normalizeHostAliases(items []HostAlias) ([]HostAlias, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 4096 {
		return nil, errors.New("server Profile has too many host aliases")
	}
	normalized := make([]HostAlias, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(item.Domain)), ".")
		if !dnsname.ValidClusterDomain(domain) {
			return nil, fmt.Errorf("invalid Server Profile host alias domain %q", item.Domain)
		}
		address, err := netip.ParseAddr(strings.TrimSpace(item.IP))
		if err != nil || !address.Is4() {
			return nil, fmt.Errorf("invalid Server Profile host alias IPv4 %q", item.IP)
		}
		if _, exists := seen[domain]; exists {
			return nil, fmt.Errorf("duplicate Server Profile host alias domain %q", domain)
		}
		seen[domain] = struct{}{}
		normalized = append(normalized, HostAlias{Domain: domain, IP: address.String()})
	}
	slices.SortFunc(normalized, func(left, right HostAlias) int {
		return strings.Compare(left.Domain, right.Domain)
	})
	return normalized, nil
}
