// Package sessionspec holds the data-plane session document exchanged over the
// desktop-to-privileged-helper RPC. It carries wire shape only: validation and
// sing-box configuration generation live in internal/singbox, which consumes
// these types, so speaking the helper protocol does not pull in the sing-box
// configuration builder.
package sessionspec

// Spec is the complete, field-constrained description accepted by the
// privileged helper. It deliberately contains no filesystem paths or commands.
type Spec struct {
	ID               string              `json:"id"`
	PodCIDRs         []string            `json:"podCIDRs,omitempty"`
	ServiceCIDRs     []string            `json:"serviceCIDRs,omitempty"`
	ServiceIPs       []string            `json:"serviceIPs,omitempty"`
	ClusterDNSServer string              `json:"clusterDNSServer,omitempty"`
	ClusterDomains   []string            `json:"clusterDomains,omitempty"`
	BridgeHost       string              `json:"bridgeHost"`
	BridgePort       int                 `json:"bridgePort"`
	ControllerPort   int                 `json:"controllerPort"`
	ControllerSecret string              `json:"controllerSecret"`
	DNSHost          string              `json:"dnsHost"`
	DNSPort          int                 `json:"dnsPort"`
	PublicDNSPort    int                 `json:"publicDNSPort"`
	TUNAddress       string              `json:"tunAddress"`
	Namespace        string              `json:"namespace,omitempty"`
	DNSNamespace     string              `json:"dnsNamespace,omitempty"`
	Namespaces       []string            `json:"namespaces,omitempty"`
	Hosts            []HostAlias         `json:"hosts,omitempty"`
	TrafficPorts     TrafficInboundPorts `json:"trafficPorts"`
	TrafficPassword  string              `json:"trafficPassword"`
	LogLevel         string              `json:"logLevel,omitempty"`
}

// DNSMeta describes the split-DNS state installed by the privileged helper.
type DNSMeta struct {
	Listen  string   `json:"listen"`
	Port    int      `json:"port"`
	Domains []string `json:"domains"`
	Search  []string `json:"search"`
	Ndots   int      `json:"ndots"`
}

// TrafficInboundPorts holds the single fixed loopback SOCKS listen port used by
// local feature adapters. Targets remain dynamic in the SOCKS request.
type TrafficInboundPorts struct {
	Listen int `json:"listen"`
}

// HostAlias maps a DNS name to an IPv4 address for the local dns-in resolver.
type HostAlias struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}
