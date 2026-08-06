package store

import (
	"fmt"
)

func (s *Store) SetConnected(contextName, namespace string, connected bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	s.state.UI.LastContext = contextName
	if namespace != "" {
		s.state.UI.LastNamespace = namespace
	}
	cluster := s.ensureClusterLocked(contextName)
	cluster.Connected = connected
	if namespace != "" {
		cluster.Namespace = namespace
	}
	// Only one context may auto-reconnect.
	if connected {
		for name, item := range s.state.Clusters {
			if name != contextName {
				item.Connected = false
			}
		}
	}
	return s.saveLocked()
}

func (s *Store) SetConnectionMode(contextName, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	if mode != "tun" && mode != "socks" {
		return fmt.Errorf("invalid connection mode %q", mode)
	}
	s.ensureClusterLocked(contextName).ConnectionMode = mode
	return s.saveLocked()
}

func (s *Store) SetPortForwards(contextName string, items []PortForwardSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	cluster := s.ensureClusterLocked(contextName)
	cluster.PortForwards = clonePortForwards(items)
	return s.saveLocked()
}

// ClearSessionIntents removes persisted port-forwards, exchanges, and mirrors
// for every context. Previews, host aliases, and network settings are kept.
func (s *Store) ClearSessionIntents() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, cluster := range s.state.Clusters {
		if cluster == nil {
			continue
		}
		if len(cluster.PortForwards) > 0 || len(cluster.Exchanges) > 0 || len(cluster.Mirrors) > 0 {
			changed = true
		}
		cluster.PortForwards = nil
		cluster.Exchanges = nil
		cluster.Mirrors = nil
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// SessionIntentCounts summarizes persisted restore intents across all contexts.
type SessionIntentCounts struct {
	PodPortForwards     int `json:"podPortForwards"`
	NetworkPortForwards int `json:"networkPortForwards"`
	Exchanges           int `json:"exchanges"`
	Mirrors             int `json:"mirrors"`
}

func (s *Store) SessionIntentCounts() SessionIntentCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var counts SessionIntentCounts
	for _, cluster := range s.state.Clusters {
		if cluster == nil {
			continue
		}
		for _, item := range cluster.PortForwards {
			if item.Kind == "pod" {
				counts.PodPortForwards++
			} else {
				counts.NetworkPortForwards++
			}
		}
		counts.Exchanges += len(cluster.Exchanges)
		counts.Mirrors += len(cluster.Mirrors)
	}
	return counts
}

func (s *Store) SetExchanges(contextName string, items []ExchangeSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	cluster := s.ensureClusterLocked(contextName)
	cluster.Exchanges = cloneExchanges(items)
	return s.saveLocked()
}

func (s *Store) SetMirrors(contextName string, items []MirrorSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	cluster := s.ensureClusterLocked(contextName)
	cluster.Mirrors = cloneMirrors(items)
	return s.saveLocked()
}

func (s *Store) SetPreviews(contextName string, items []PreviewSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	cluster := s.ensureClusterLocked(contextName)
	cluster.Previews = clonePreviews(items)
	return s.saveLocked()
}

func (s *Store) HostAliases(contextName string) []HostAliasSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.state.Clusters[contextName]
	if item == nil {
		return nil
	}
	return cloneHostAliases(item.HostAliases)
}

// SetHostAliases replaces host aliases for a context.
// An empty or nil list clears the stored configuration.
func (s *Store) SetHostAliases(contextName string, items []HostAliasSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	cluster := s.ensureClusterLocked(contextName)
	if len(items) == 0 {
		cluster.HostAliases = nil
	} else {
		cluster.HostAliases = cloneHostAliases(items)
	}
	return s.saveLocked()
}

func (s *Store) ManualNetwork(contextName string) ManualNetwork {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.state.Clusters[contextName]
	if item == nil || item.ManualNetwork == nil {
		return ManualNetwork{}
	}
	return cloneManualNetwork(*item.ManualNetwork)
}

func (s *Store) SetManualNetwork(contextName string, network ManualNetwork) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextName == "" {
		return nil
	}
	cluster := s.ensureClusterLocked(contextName)
	if len(network.PodCIDRs) == 0 && len(network.ServiceCIDRs) == 0 &&
		network.DNSServer == "" && len(network.ClusterDomains) == 0 &&
		network.DNSNamespace == "" {
		cluster.ManualNetwork = nil
	} else {
		copyItem := cloneManualNetwork(network)
		cluster.ManualNetwork = &copyItem
	}
	return s.saveLocked()
}

func (s *Store) Cluster(contextName string) ClusterState {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.state.Clusters[contextName]
	if item == nil {
		return ClusterState{}
	}
	return cloneCluster(*item)
}

func (s *Store) ensureClusterLocked(contextName string) *ClusterState {
	if s.state.Clusters == nil {
		s.state.Clusters = map[string]*ClusterState{}
	}
	item := s.state.Clusters[contextName]
	if item == nil {
		item = &ClusterState{}
		s.state.Clusters[contextName] = item
	}
	return item
}
