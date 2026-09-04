package singbox

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

const maxSessionItems = 4096

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

// Validate reports whether spec is a well-formed session document.
func Validate(s sessionspec.Spec) error {
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
	if len(s.PodCIDRs)+len(s.ServiceCIDRs)+len(s.ServiceIPs)+len(s.Namespaces)+len(s.Hosts) > maxSessionItems {
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
	if s.Namespace != "" && !dns.ValidLabel(s.Namespace) {
		return errors.New("invalid namespace")
	}
	if s.DNSNamespace != "" && !dns.ValidLabel(s.DNSNamespace) {
		return errors.New("invalid DNS namespace")
	}
	for _, namespace := range s.Namespaces {
		if !dns.ValidLabel(namespace) {
			return errors.New("invalid namespace in namespace list")
		}
	}
	if s.ClusterDNSServer != "" {
		if _, err := netip.ParseAddr(s.ClusterDNSServer); err != nil {
			return fmt.Errorf("invalid cluster DNS address: %w", err)
		}
	}
	if s.LogLevel != "" {
		if _, ok := validLogLevels[strings.ToLower(strings.TrimSpace(s.LogLevel))]; !ok {
			return errors.New("invalid log level")
		}
	}
	if _, err := dns.NormalizeClusterDomains(s.ClusterDomains); err != nil {
		return err
	}
	if _, err := NormalizeHostAliases(s.Hosts); err != nil {
		return err
	}
	_, err = clusterRoutes(discovery(s))
	return err
}

func ValidateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return errors.New("invalid session ID")
	}
	return nil
}

// GenerateConfig renders the sing-box configuration for spec.
func GenerateConfig(s sessionspec.Spec) ([]byte, error) {
	if err := Validate(s); err != nil {
		return nil, err
	}
	hosts, _ := NormalizeHostAliases(s.Hosts)
	domains, _ := dns.NormalizeClusterDomains(s.ClusterDomains)
	return Generate(discovery(s), Options{
		BridgeHost:       s.BridgeHost,
		BridgePort:       s.BridgePort,
		ControllerPort:   s.ControllerPort,
		ControllerSecret: s.ControllerSecret,
		DNSHost:          s.DNSHost,
		DNSPort:          s.DNSPort,
		TUNAddress:       s.TUNAddress,
		Namespace:        dnsNamespace(s),
		ClusterDomains:   domains,
		Hosts:            hosts,
		TrafficPorts:     s.TrafficPorts,
		TrafficPassword:  s.TrafficPassword,
		LogLevel:         s.LogLevel,
	})
}

// Routes reports the cluster routes spec installs.
func Routes(s sessionspec.Spec) ([]string, error) {
	if err := Validate(s); err != nil {
		return nil, err
	}
	return clusterRoutes(discovery(s))
}

// DNS reports the split-DNS state spec installs.
func DNS(s sessionspec.Spec) (sessionspec.DNSMeta, error) {
	if err := Validate(s); err != nil {
		return sessionspec.DNSMeta{}, err
	}
	hosts, _ := NormalizeHostAliases(s.Hosts)
	domains, _ := dns.NormalizeClusterDomains(s.ClusterDomains)
	ns := dnsNamespace(s)
	return sessionspec.DNSMeta{
		Listen:  s.DNSHost,
		Port:    s.PublicDNSPort,
		Domains: ResolverDomains(ns, domains, hosts),
		Search:  SearchDomains(ns, domains...),
		Ndots:   1,
	}, nil
}

func dnsNamespace(s sessionspec.Spec) string {
	return cmp.Or(s.DNSNamespace, s.Namespace, defaultNamespace)
}

func discovery(s sessionspec.Spec) NetworkSpec {
	domains, _ := dns.NormalizeClusterDomains(s.ClusterDomains)
	return NetworkSpec{
		PodCIDRs:       s.PodCIDRs,
		ServiceCIDRs:   s.ServiceCIDRs,
		ServiceIPs:     s.ServiceIPs,
		DNSServer:      s.ClusterDNSServer,
		ClusterDomains: domains,
		Namespaces:     slices.Clone(s.Namespaces),
	}
}

func validateLoopback(raw, label string) error {
	ip, err := netip.ParseAddr(raw)
	if err != nil || !ip.IsLoopback() {
		return fmt.Errorf("%s host must be a loopback IP address", label)
	}
	return nil
}
