package singbox

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"k8s.io/apimachinery/pkg/util/validation"
)

const maxSessionItems = 4096

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

// SessionSpec is the complete, field-constrained description accepted by the
// privileged helper. It deliberately contains no filesystem paths or commands.
type SessionSpec struct {
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
	Hosts            []HostAlias         `json:"hosts,omitempty"`
	TrafficPorts     TrafficInboundPorts `json:"trafficPorts"`
	TrafficPassword  string              `json:"trafficPassword"`
}

// DNSMeta describes the split-DNS state installed by the privileged helper.
type DNSMeta struct {
	Listen  string   `json:"listen"`
	Port    int      `json:"port"`
	Domains []string `json:"domains"`
	Search  []string `json:"search"`
	Ndots   int      `json:"ndots"`
}

func (s SessionSpec) Validate() error {
	if err := ValidateSessionID(s.ID); err != nil {
		return err
	}
	if err := validateLoopback(s.BridgeHost, "bridge"); err != nil {
		return err
	}
	if err := validateLoopback(s.DNSHost, "DNS"); err != nil {
		return err
	}
	if err := validatePort(s.BridgePort, "bridge"); err != nil {
		return err
	}
	if err := validatePort(s.ControllerPort, "controller"); err != nil {
		return err
	}
	if err := validatePort(s.DNSPort, "DNS"); err != nil {
		return err
	}
	if err := validatePort(s.PublicDNSPort, "public DNS"); err != nil {
		return err
	}
	if len(s.ControllerSecret) < 32 || len(s.ControllerSecret) > 256 {
		return errors.New("controller secret must be between 32 and 256 characters")
	}
	if len(s.TrafficPassword) < 32 || len(s.TrafficPassword) > 255 {
		return errors.New("traffic password must be between 32 and 255 characters")
	}
	if err := validatePort(s.TrafficPorts.Listen, TrafficInbound); err != nil {
		return err
	}
	if slices.Contains([]int{s.BridgePort, s.ControllerPort, s.DNSPort, s.PublicDNSPort}, s.TrafficPorts.Listen) {
		return errors.New("traffic inbound port must not overlap internal ports")
	}
	if len(s.PodCIDRs)+len(s.ServiceCIDRs)+len(s.ServiceIPs)+len(s.Hosts) > maxSessionItems {
		return errors.New("session contains too many routes or host aliases")
	}
	tun, err := netip.ParsePrefix(s.TUNAddress)
	if err != nil {
		return fmt.Errorf("invalid TUN address %q: %w", s.TUNAddress, err)
	}
	benchmark := netip.MustParsePrefix("198.18.0.0/15")
	if !tun.Addr().Is4() || tun.Bits() != 30 || !benchmark.Contains(tun.Addr()) ||
		tun.Addr() == tun.Masked().Addr() {
		return errors.New("TUN address must be a host in a 198.18.0.0/15 /30 subnet")
	}
	if s.Namespace != "" && len(validation.IsDNS1123Label(s.Namespace)) != 0 {
		return errors.New("invalid namespace")
	}
	if s.DNSNamespace != "" && len(validation.IsDNS1123Label(s.DNSNamespace)) != 0 {
		return errors.New("invalid DNS namespace")
	}
	if s.ClusterDNSServer != "" {
		if _, err := netip.ParseAddr(s.ClusterDNSServer); err != nil {
			return fmt.Errorf("invalid cluster DNS address: %w", err)
		}
	}
	if _, err := dnsname.NormalizeClusterDomains(s.ClusterDomains); err != nil {
		return err
	}
	if _, err := NormalizeHostAliases(s.Hosts); err != nil {
		return err
	}
	_, err = clusterRoutes(s.discovery())
	return err
}

func ValidateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return errors.New("invalid session ID")
	}
	return nil
}

func (s SessionSpec) GenerateConfig() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	hosts, _ := NormalizeHostAliases(s.Hosts)
	domains, _ := dnsname.NormalizeClusterDomains(s.ClusterDomains)
	return Generate(s.discovery(), Options{
		BridgeHost:       s.BridgeHost,
		BridgePort:       s.BridgePort,
		ControllerPort:   s.ControllerPort,
		ControllerSecret: s.ControllerSecret,
		DNSHost:          s.DNSHost,
		DNSPort:          s.DNSPort,
		TUNAddress:       s.TUNAddress,
		Namespace:        s.dnsNamespace(),
		ClusterDomains:   domains,
		Hosts:            hosts,
		TrafficPorts:     s.TrafficPorts,
		TrafficPassword:  s.TrafficPassword,
	})
}

func (s SessionSpec) Routes() ([]string, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return clusterRoutes(s.discovery())
}

func (s SessionSpec) DNS() (DNSMeta, error) {
	if err := s.Validate(); err != nil {
		return DNSMeta{}, err
	}
	hosts, _ := NormalizeHostAliases(s.Hosts)
	domains, _ := dnsname.NormalizeClusterDomains(s.ClusterDomains)
	ns := s.dnsNamespace()
	return DNSMeta{
		Listen:  s.DNSHost,
		Port:    s.PublicDNSPort,
		Domains: ResolverDomains(ns, domains, hosts),
		Search:  SearchDomains(ns, domains...),
		Ndots:   5,
	}, nil
}

func (s SessionSpec) dnsNamespace() string {
	return cmp.Or(s.DNSNamespace, s.Namespace, "default")
}

func (s SessionSpec) discovery() NetworkSpec {
	domains, _ := dnsname.NormalizeClusterDomains(s.ClusterDomains)
	return NetworkSpec{
		PodCIDRs:       s.PodCIDRs,
		ServiceCIDRs:   s.ServiceCIDRs,
		ServiceIPs:     s.ServiceIPs,
		DNSServer:      s.ClusterDNSServer,
		ClusterDomains: domains,
	}
}

func validateLoopback(raw, label string) error {
	ip, err := netip.ParseAddr(raw)
	if err != nil || !ip.IsLoopback() {
		return fmt.Errorf("%s host must be a loopback IP address", label)
	}
	return nil
}
