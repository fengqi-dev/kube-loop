package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
)

// ShareGateway reports whether this client uses the cluster-wide shared
// Gateway. It defaults to true for compatibility with existing state files.
func (s *Store) ShareGateway() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Settings.ShareGateway == nil || *s.state.Settings.ShareGateway
}

// SetShareGateway updates the Gateway sharing preference. A stable opaque ID
// is allocated the first time a private Gateway is requested.
func (s *Store) SetShareGateway(shared bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	gatewayID := s.state.Settings.GatewayID
	if !shared && s.state.Settings.GatewayID == "" {
		var id [5]byte
		if _, err := rand.Read(id[:]); err != nil {
			return fmt.Errorf("generate private gateway id: %w", err)
		}
		gatewayID = hex.EncodeToString(id[:])
	}
	s.state.Settings.ShareGateway = new(shared)
	s.state.Settings.GatewayID = gatewayID
	return s.saveLocked()
}

func (s *Store) GatewayID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Settings.GatewayID
}

func (s *Store) GatewayNamespace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Settings.GatewayNamespace
}

func (s *Store) SetGatewayNamespace(namespace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Settings.GatewayNamespace = namespace
	return s.saveLocked()
}

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
