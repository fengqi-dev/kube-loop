package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

const (
	currentVersion = 1
	maxStateBytes  = 1 << 20
)

type State struct {
	Version         int       `json:"version"`
	ActiveProfileID string    `json:"activeProfileId,omitempty"`
	Profiles        []Profile `json:"profiles"`
}

type Profile struct {
	ID             string      `json:"id"`
	BaseURL        string      `json:"baseUrl"`
	TunnelPath     string      `json:"tunnelPath"`
	DisplayName    string      `json:"displayName,omitempty"`
	LastIdentityID string      `json:"lastIdentityId,omitempty"`
	LastUserName   string      `json:"lastUserName,omitempty"`
	LastNamespace  string      `json:"lastNamespace,omitempty"`
	DNSNamespace   string      `json:"dnsNamespace,omitempty"`
	SOCKSPort      int         `json:"socksPort,omitempty"`
	HostAliases    []HostAlias `json:"hostAliases,omitempty"`
}

type HostAlias struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}

type Store struct {
	path      string
	mu        sync.Mutex
	state     State
	recovered bool
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("find user home directory")
	}
	return filepath.Join(home, ".kubeloop", "servers.json"), nil
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve Server Profile store path")
	}
	store := &Store{path: absolute, state: State{Version: currentVersion, Profiles: []Profile{}}}
	if err := store.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return store, nil
}

func (store *Store) Path() string { return store.path }
func (store *Store) RecoveredFromBackup() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.recovered
}

func (store *Store) Snapshot() State {
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneState(store.state)
}

func (store *Store) Upsert(profile Profile) error {
	normalized, err := normalizeProfile(profile)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	next := cloneState(store.state)
	index := slices.IndexFunc(next.Profiles, func(item Profile) bool { return item.ID == normalized.ID })
	if index >= 0 {
		next.Profiles[index] = normalized
	} else {
		next.Profiles = append(next.Profiles, normalized)
	}
	if next.ActiveProfileID == "" {
		next.ActiveProfileID = normalized.ID
	}
	return store.saveLocked(next)
}

func (store *Store) Remove(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("Server Profile ID is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	next := cloneState(store.state)
	index := slices.IndexFunc(next.Profiles, func(item Profile) bool { return item.ID == id })
	if index < 0 {
		return errors.New("Server Profile not found")
	}
	next.Profiles = append(next.Profiles[:index], next.Profiles[index+1:]...)
	if next.ActiveProfileID == id {
		next.ActiveProfileID = ""
		if len(next.Profiles) > 0 {
			next.ActiveProfileID = next.Profiles[0].ID
		}
	}
	return store.saveLocked(next)
}

func (store *Store) SetActive(id string) error {
	id = strings.TrimSpace(id)
	store.mu.Lock()
	defer store.mu.Unlock()
	if !slices.ContainsFunc(store.state.Profiles, func(item Profile) bool { return item.ID == id }) {
		return errors.New("Server Profile not found")
	}
	next := cloneState(store.state)
	next.ActiveProfileID = id
	return store.saveLocked(next)
}

func (store *Store) load() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := readState(store.path)
	if err == nil {
		store.state = state
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return err
	}
	backup, backupErr := readState(store.path + ".bak")
	if backupErr != nil {
		return fmt.Errorf("load Server Profile store: %w", err)
	}
	store.state = backup
	store.recovered = true
	return nil
}

func (store *Store) saveLocked(next State) error {
	normalized, err := normalizeState(next)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return errors.New("encode Server Profile store")
	}
	raw = append(raw, '\n')
	if existing, err := os.ReadFile(store.path); err == nil {
		if _, decodeErr := decodeState(existing); decodeErr == nil {
			if err := fsatomic.WriteFile(store.path+".bak", existing, 0o700, 0o600); err != nil {
				return fmt.Errorf("backup Server Profile store: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("read existing Server Profile store")
	}
	if err := fsatomic.WriteFile(store.path, raw, 0o700, 0o600); err != nil {
		return fmt.Errorf("save Server Profile store: %w", err)
	}
	store.state = normalized
	store.recovered = false
	return nil
}

func readState(path string) (State, error) {
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil {
		return State{}, errors.New("read Server Profile store")
	}
	if len(raw) > maxStateBytes {
		return State{}, errors.New("Server Profile store exceeds 1 MiB")
	}
	return decodeState(raw)
}

func decodeState(raw []byte) (State, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, errors.New("decode Server Profile store")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return State{}, errors.New("Server Profile store must contain one JSON document")
	}
	return normalizeState(state)
}

func normalizeState(state State) (State, error) {
	if state.Version != currentVersion {
		return State{}, fmt.Errorf("unsupported Server Profile store version %d", state.Version)
	}
	seen := make(map[string]struct{}, len(state.Profiles))
	for index, item := range state.Profiles {
		normalized, err := normalizeProfile(item)
		if err != nil {
			return State{}, fmt.Errorf("Server Profile %d: %w", index, err)
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
		return Profile{}, errors.New("Server Profile SOCKS port must be between 1 and 65535")
	}
	if profile.DNSNamespace != "" && !dnsname.ValidLabel(profile.DNSNamespace) {
		return Profile{}, errors.New("Server Profile DNS namespace is invalid")
	}
	aliases, err := normalizeHostAliases(profile.HostAliases)
	if err != nil {
		return Profile{}, err
	}
	profile.HostAliases = aliases
	if profile.ID == "" || len(profile.ID) > 128 {
		return Profile{}, errors.New("Server Profile ID must contain 1-128 characters")
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
	if err != nil || !strings.HasPrefix(profile.TunnelPath, "/") || parsedTunnelPath.IsAbs() || parsedTunnelPath.Host != "" ||
		parsedTunnelPath.RawQuery != "" || parsedTunnelPath.Fragment != "" || parsedTunnelPath.EscapedPath() != profile.TunnelPath ||
		strings.Contains(profile.TunnelPath, "//") || strings.Contains(profile.TunnelPath, "/./") || strings.Contains(profile.TunnelPath, "/../") ||
		strings.HasSuffix(profile.TunnelPath, "/.") || strings.HasSuffix(profile.TunnelPath, "/..") {
		return Profile{}, errors.New("Server Profile tunnel path is invalid")
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

func cloneState(state State) State {
	cloned := State{
		Version: state.Version, ActiveProfileID: state.ActiveProfileID,
		Profiles: append([]Profile{}, state.Profiles...),
	}
	for index := range cloned.Profiles {
		cloned.Profiles[index].HostAliases = append([]HostAlias{}, cloned.Profiles[index].HostAliases...)
	}
	return cloned
}

func normalizeHostAliases(items []HostAlias) ([]HostAlias, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 4096 {
		return nil, errors.New("Server Profile has too many host aliases")
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
