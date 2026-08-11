// Package config loads deployment-owned Controller Management Plane settings.
// Secret material is never accepted in this document; it contains only exact
// bootstrap identities and a mounted break-glass Secret alias/file reference.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
)

const (
	MaximumConfigBytes          = 1 << 20
	DefaultBreakGlassSessionTTL = 15 * time.Minute
	MinimumBreakGlassSessionTTL = time.Minute
	breakGlassMountRoot         = "/var/run/secrets/kubeloop/management/break-glass"
	providerSecretMountRoot     = "/var/run/secrets/kubeloop/management/providers"
	maximumProviderAliases      = 64
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type File struct {
	Bootstrap             BootstrapConfig       `json:"bootstrap"`
	BreakGlass            BreakGlassConfig      `json:"breakGlass"`
	ProviderSecretAliases ProviderSecretAliases `json:"providerSecretAliases"`
}

type ProviderSecretAliases map[string]ProviderSecretAlias

type ProviderSecretAlias struct {
	ClientSecretFile string `json:"clientSecretFile,omitempty"`
	BindPasswordFile string `json:"bindPasswordFile,omitempty"`
	CAFile           string `json:"caFile,omitempty"`
}

func (aliases ProviderSecretAliases) Resolve(alias, use string) (string, error) {
	entry, ok := aliases[strings.TrimSpace(alias)]
	if !ok {
		return "", errors.New("managed Provider Secret alias is not allowlisted")
	}
	var path string
	switch strings.TrimSpace(use) {
	case "client-secret":
		path = entry.ClientSecretFile
	case "bind-password":
		path = entry.BindPasswordFile
	case "ca":
		path = entry.CAFile
	default:
		return "", errors.New("managed Provider Secret use is invalid")
	}
	if path == "" {
		return "", errors.New("managed Provider Secret use is not allowlisted")
	}
	return path, nil
}

type BootstrapConfig struct {
	Subjects        []string `json:"subjects"`
	Groups          []string `json:"groups"`
	RecoveryEnabled bool     `json:"recoveryEnabled"`
}

func (config BootstrapConfig) AuthorizationConfig() adminauthorization.BootstrapConfig {
	return adminauthorization.BootstrapConfig{
		Subjects: append([]string(nil), config.Subjects...), Groups: append([]string(nil), config.Groups...),
		RecoveryEnabled: config.RecoveryEnabled,
	}
}

type BreakGlassConfig struct {
	Enabled            bool     `json:"enabled"`
	SecretAlias        string   `json:"secretAlias"`
	SecretFile         string   `json:"secretFile"`
	SessionTTL         string   `json:"sessionTtl"`
	AllowedSourceCIDRs []string `json:"allowedSourceCidrs"`

	sessionTTL  time.Duration
	sourceCIDRs []netip.Prefix
}

func (config BreakGlassConfig) ParsedSessionTTL() time.Duration {
	return config.sessionTTL
}

func (config BreakGlassConfig) ParsedSourceCIDRs() []netip.Prefix {
	return append([]netip.Prefix(nil), config.sourceCIDRs...)
}

func Load(path string) (File, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return normalize(File{})
	}
	file, err := os.Open(path)
	if err != nil {
		return File{}, errors.New("open management configuration")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, MaximumConfigBytes+1))
	if err != nil {
		return File{}, errors.New("read management configuration")
	}
	if len(raw) > MaximumConfigBytes {
		return File{}, errors.New("management configuration exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config File
	if err := decoder.Decode(&config); err != nil {
		return File{}, errors.New("decode management configuration")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return File{}, errors.New("management configuration must contain one JSON document")
	}
	return normalize(config)
}

func normalize(config File) (File, error) {
	if config.Bootstrap.Subjects == nil {
		config.Bootstrap.Subjects = []string{}
	}
	if config.Bootstrap.Groups == nil {
		config.Bootstrap.Groups = []string{}
	}
	if _, err := adminauthorization.NewDenyAll(adminauthorization.WithBootstrap(
		config.Bootstrap.AuthorizationConfig(), nil,
	)); err != nil {
		return File{}, fmt.Errorf("invalid management bootstrap configuration: %w", err)
	}
	breakGlass, err := normalizeBreakGlass(config.BreakGlass)
	if err != nil {
		return File{}, err
	}
	config.BreakGlass = breakGlass
	providerAliases, err := normalizeProviderSecretAliases(config.ProviderSecretAliases)
	if err != nil {
		return File{}, err
	}
	config.ProviderSecretAliases = providerAliases
	return config, nil
}

func normalizeProviderSecretAliases(aliases ProviderSecretAliases) (ProviderSecretAliases, error) {
	if aliases == nil {
		return ProviderSecretAliases{}, nil
	}
	if len(aliases) > maximumProviderAliases {
		return nil, errors.New("management Provider Secret aliases exceed 64 entries")
	}
	result := make(ProviderSecretAliases, len(aliases))
	for alias, entry := range aliases {
		if !aliasPattern.MatchString(alias) {
			return nil, fmt.Errorf("management Provider Secret alias %q is invalid", alias)
		}
		entry.ClientSecretFile = strings.TrimSpace(entry.ClientSecretFile)
		entry.BindPasswordFile = strings.TrimSpace(entry.BindPasswordFile)
		entry.CAFile = strings.TrimSpace(entry.CAFile)
		if entry.ClientSecretFile == "" && entry.BindPasswordFile == "" && entry.CAFile == "" {
			return nil, fmt.Errorf("management Provider Secret alias %q has no projected keys", alias)
		}
		root := path.Join(providerSecretMountRoot, alias)
		for use, actual := range map[string]string{
			"client-secret": entry.ClientSecretFile,
			"bind-password": entry.BindPasswordFile,
			"ca.crt":        entry.CAFile,
		} {
			if actual != "" && actual != path.Join(root, use) {
				return nil, fmt.Errorf("management Provider Secret alias %q must use fixed mount paths", alias)
			}
		}
		result[alias] = entry
	}
	return result, nil
}

func normalizeBreakGlass(config BreakGlassConfig) (BreakGlassConfig, error) {
	config.SecretAlias = strings.TrimSpace(config.SecretAlias)
	config.SecretFile = strings.TrimSpace(config.SecretFile)
	config.SessionTTL = strings.TrimSpace(config.SessionTTL)
	if !config.Enabled {
		if config.SecretAlias != "" || config.SecretFile != "" || len(config.AllowedSourceCIDRs) != 0 {
			return BreakGlassConfig{}, errors.New("disabled management break-glass must not reference a Secret or source CIDR")
		}
		if config.SessionTTL != "" && config.SessionTTL != DefaultBreakGlassSessionTTL.String() {
			return BreakGlassConfig{}, errors.New("disabled management break-glass must not override session TTL")
		}
		config.SessionTTL = DefaultBreakGlassSessionTTL.String()
		config.sessionTTL = DefaultBreakGlassSessionTTL
		config.AllowedSourceCIDRs = []string{}
		config.sourceCIDRs = []netip.Prefix{}
		return config, nil
	}
	if !aliasPattern.MatchString(config.SecretAlias) {
		return BreakGlassConfig{}, errors.New("management break-glass requires a valid Secret alias")
	}
	wantSecretFile := path.Join(breakGlassMountRoot, config.SecretAlias, "credential")
	if config.SecretFile != wantSecretFile {
		return BreakGlassConfig{}, errors.New("management break-glass Secret file must use the fixed Controller mount path")
	}
	if config.SessionTTL == "" {
		config.SessionTTL = DefaultBreakGlassSessionTTL.String()
	}
	ttl, err := time.ParseDuration(config.SessionTTL)
	if err != nil || ttl < MinimumBreakGlassSessionTTL || ttl > DefaultBreakGlassSessionTTL {
		return BreakGlassConfig{}, errors.New("management break-glass session TTL must be between 1m and 15m")
	}
	config.sessionTTL = ttl
	if len(config.AllowedSourceCIDRs) > 32 {
		return BreakGlassConfig{}, errors.New("management break-glass source CIDRs exceed 32 entries")
	}
	config.sourceCIDRs = make([]netip.Prefix, 0, len(config.AllowedSourceCIDRs))
	seen := make(map[netip.Prefix]struct{}, len(config.AllowedSourceCIDRs))
	for index, raw := range config.AllowedSourceCIDRs {
		prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(raw))
		if parseErr != nil || prefix != prefix.Masked() {
			return BreakGlassConfig{}, fmt.Errorf("management break-glass source CIDR %d is invalid", index)
		}
		if _, exists := seen[prefix]; exists {
			return BreakGlassConfig{}, fmt.Errorf("management break-glass source CIDR %d is duplicated", index)
		}
		seen[prefix] = struct{}{}
		config.sourceCIDRs = append(config.sourceCIDRs, prefix)
		config.AllowedSourceCIDRs[index] = prefix.String()
	}
	if config.AllowedSourceCIDRs == nil {
		config.AllowedSourceCIDRs = []string{}
	}
	return config, nil
}
