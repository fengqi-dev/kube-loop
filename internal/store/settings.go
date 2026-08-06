package store

import (
	"fmt"
	"slices"
)

func (s *Store) MCP() MCPConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalizeMCP(s.state.MCP)
}

// SetMCP replaces the embedded MCP server configuration.
func (s *Store) SetMCP(cfg MCPConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.MCP = normalizeMCP(cfg)
	return s.saveLocked()
}

func (s *Store) SetUI(contextName, namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.UI.LastContext = contextName
	s.state.UI.LastNamespace = namespace
	if contextName != "" {
		cluster := s.ensureClusterLocked(contextName)
		if namespace != "" {
			cluster.Namespace = namespace
		}
	}
	return s.saveLocked()
}

// KubeconfigFiles returns a copy of user-added kubeconfig paths.
func (s *Store) KubeconfigFiles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStrings(s.state.UI.KubeconfigFiles)
}

// AddKubeconfigFile appends an absolute kubeconfig path if not already present.
func (s *Store) AddKubeconfigFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := normalizeStorePath(path)
	if err != nil {
		return err
	}
	if slices.Contains(s.state.UI.KubeconfigFiles, path) {
		return nil
	}
	s.state.UI.KubeconfigFiles = append(s.state.UI.KubeconfigFiles, path)
	return s.saveLocked()
}

// RemoveKubeconfigFile removes a user-added kubeconfig path.
func (s *Store) RemoveKubeconfigFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := normalizeStorePath(path)
	if err != nil {
		return err
	}
	files := s.state.UI.KubeconfigFiles
	next := make([]string, 0, len(files))
	for _, existing := range files {
		if existing != path {
			next = append(next, existing)
		}
	}
	if len(next) == len(files) {
		return fmt.Errorf("kubeconfig file not found: %s", path)
	}
	if len(next) == 0 {
		s.state.UI.KubeconfigFiles = nil
	} else {
		s.state.UI.KubeconfigFiles = next
	}
	return s.saveLocked()
}

func normalizeMCP(cfg MCPConfig) MCPConfig {
	if cfg.Port <= 0 {
		cfg.Port = DefaultMCPPort
	}
	return cfg
}
