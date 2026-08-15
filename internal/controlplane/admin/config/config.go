// Package config loads deployment-owned Control Plane Management Plane settings.
// It contains only the optional mounted break-glass Secret reference.
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
)

const (
	MaximumConfigBytes          = 1 << 20
	DefaultBreakGlassSessionTTL = 15 * time.Minute
	MinimumBreakGlassSessionTTL = time.Minute
	breakGlassMountRoot         = "/var/run/secrets/kubeloop/management/break-glass"
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type File struct {
	BreakGlass BreakGlassConfig `json:"breakGlass"`
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
		return Normalize(File{})
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
	return Normalize(config)
}

// Normalize validates and applies defaults to deployment-owned management settings.
func Normalize(config File) (File, error) {
	breakGlass, err := normalizeBreakGlass(config.BreakGlass)
	if err != nil {
		return File{}, err
	}
	config.BreakGlass = breakGlass
	return config, nil
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
		return BreakGlassConfig{}, errors.New("management break-glass Secret file must use the fixed Control Plane mount path")
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
