package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

// Store persists State as a JSON file.
type Store struct {
	path string

	mu    sync.Mutex
	state State
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home directory: %w", err)
	}
	return filepath.Join(home, ".kubeloop", "state.json"), nil
}

func Open(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	store := &Store{
		path: path,
		state: State{
			Version:  currentVersion,
			Clusters: map[string]*ClusterState{},
		},
	}
	if err := store.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return store, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	if state.Clusters == nil {
		state.Clusters = map[string]*ClusterState{}
	}
	if state.Version == 0 {
		state.Version = currentVersion
	}
	s.state = state
	return nil
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state)
}

// MCP returns a copy of the embedded MCP server configuration.

func (s *Store) saveLocked() error {
	s.state.Version = currentVersion
	if s.state.Clusters == nil {
		s.state.Clusters = map[string]*ClusterState{}
	}
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	raw = append(raw, '\n')
	if err := fsatomic.WriteFile(s.path, raw, 0o755, 0o600); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

func normalizeStorePath(path string) (string, error) {
	path = filepath.Clean(path)
	if path == "" || path == "." {
		return "", fmt.Errorf("kubeconfig path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve kubeconfig path: %w", err)
	}
	return abs, nil
}
