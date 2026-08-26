package profile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/utils"
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
	writeMu   sync.Mutex
	mu        sync.RWMutex
	state     State
	recovered bool
	writeFile func(string, []byte, os.FileMode, os.FileMode) error
}

func DefaultPath() (string, error) {
	layout, err := utils.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.ConfigDir(), "servers.json"), nil
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
	store := &Store{
		path: absolute, state: State{Version: currentVersion, Profiles: []Profile{}},
		writeFile: utils.WriteFile,
	}
	if err := store.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return store, nil
}

func (store *Store) Path() string { return store.path }
func (store *Store) RecoveredFromBackup() bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.recovered
}

func (store *Store) Snapshot() State {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneState(store.state)
}

func (store *Store) Upsert(profile Profile) error {
	normalized, err := normalizeProfile(profile)
	if err != nil {
		return err
	}
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	store.mu.RLock()
	next := cloneState(store.state)
	store.mu.RUnlock()
	index := slices.IndexFunc(next.Profiles, func(item Profile) bool { return item.ID == normalized.ID })
	if index >= 0 {
		next.Profiles[index] = normalized
	} else {
		next.Profiles = append(next.Profiles, normalized)
	}
	if next.ActiveProfileID == "" {
		next.ActiveProfileID = normalized.ID
	}
	return store.save(next)
}

func (store *Store) Remove(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("server Profile ID is required")
	}
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	store.mu.RLock()
	next := cloneState(store.state)
	store.mu.RUnlock()
	index := slices.IndexFunc(next.Profiles, func(item Profile) bool { return item.ID == id })
	if index < 0 {
		return errors.New("server Profile not found")
	}
	next.Profiles = append(next.Profiles[:index], next.Profiles[index+1:]...)
	if next.ActiveProfileID == id {
		next.ActiveProfileID = ""
		if len(next.Profiles) > 0 {
			next.ActiveProfileID = next.Profiles[0].ID
		}
	}
	return store.save(next)
}

func (store *Store) SetActive(id string) error {
	id = strings.TrimSpace(id)
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	store.mu.RLock()
	if !slices.ContainsFunc(store.state.Profiles, func(item Profile) bool { return item.ID == id }) {
		store.mu.RUnlock()
		return errors.New("server Profile not found")
	}
	next := cloneState(store.state)
	store.mu.RUnlock()
	next.ActiveProfileID = id
	return store.save(next)
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
