package store

const currentVersion = 1

// State is the on-disk multi-cluster client state.
type State struct {
	Version  int                      `json:"version"`
	UI       UIState                  `json:"ui"`
	MCP      MCPConfig                `json:"mcp,omitzero"`
	Clusters map[string]*ClusterState `json:"clusters"`
}

// DefaultMCPPort is the Streamable HTTP listen port when unset.
const DefaultMCPPort = 30808

// MCPConfig persists the embedded MCP server settings.
type MCPConfig struct {
	Enabled      bool   `json:"enabled,omitzero"`
	Port         int    `json:"port,omitzero"`         // default DefaultMCPPort
	TokenEnabled bool   `json:"tokenEnabled,omitzero"` // default false; require Bearer token
	Token        string `json:"token,omitempty"`       // opaque bearer (used when TokenEnabled)
}

// UIState remembers the last selected context in the desktop UI.
type UIState struct {
	LastContext     string   `json:"lastContext,omitempty"`
	LastNamespace   string   `json:"lastNamespace,omitempty"`
	KubeconfigFiles []string `json:"kubeconfigFiles,omitempty"` // absolute paths, user-added
}

// ClusterState stores restore intents for one kubeconfig context.
type ClusterState struct {
	Namespace      string            `json:"namespace,omitempty"`
	ConnectionMode string            `json:"connectionMode,omitempty"`
	Connected      bool              `json:"connected,omitzero"`
	PortForwards   []PortForwardSpec `json:"portForwards,omitempty"`
	Exchanges      []ExchangeSpec    `json:"exchanges,omitempty"`
	Mirrors        []MirrorSpec      `json:"mirrors,omitempty"`
	Previews       []PreviewSpec     `json:"previews,omitempty"`
	HostAliases    []HostAliasSpec   `json:"hostAliases,omitempty"`
	ManualNetwork  *ManualNetwork    `json:"manualNetwork,omitempty"`
}

// HostAliasSpec maps a DNS name to an IPv4 address for the local tunnel DNS.
type HostAliasSpec struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}

// ManualNetwork is user-supplied Pod/Service CIDR and CoreDNS when auto-discovery fails.
type ManualNetwork struct {
	PodCIDRs       []string `json:"podCIDRs,omitempty"`
	ServiceCIDRs   []string `json:"serviceCIDRs,omitempty"`
	DNSServer      string   `json:"dnsServer,omitempty"`
	ClusterDomains []string `json:"clusterDomains,omitempty"`
	DNSNamespace   string   `json:"dnsNamespace,omitempty"`
}

// PortForwardSpec is a port-forward intent (not runtime listen state).
type PortForwardSpec struct {
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol,omitempty"`
	RemotePort uint16 `json:"remotePort"`
	LocalPort  uint16 `json:"localPort,omitzero"`
}

// PortMapping maps a service/local port pair.
type PortMapping struct {
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
	LocalHost   string `json:"localHost"`
	LocalPort   int    `json:"localPort"`
}

// ExchangeSpec replaces an existing Service with a local process.
type ExchangeSpec struct {
	Namespace string        `json:"namespace"`
	Service   string        `json:"service"`
	Ports     []PortMapping `json:"ports"`
}

// MirrorSpec hijacks a Service through the Gateway while keeping cluster
// Pods as the primary response path and teeing requests to a local process.
type MirrorSpec struct {
	Namespace string        `json:"namespace"`
	Service   string        `json:"service"`
	Ports     []PortMapping `json:"ports"`
}

// PreviewSpec exposes a local process as a new ClusterIP Service.
type PreviewSpec struct {
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Ports     []PortMapping `json:"ports"`
}
