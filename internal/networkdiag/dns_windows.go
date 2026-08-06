//go:build windows

package networkdiag

import (
	"fmt"
	"net"
)

func inspectDNSPort() *Issue {
	const address = "127.0.0.1:53"
	tcp, tcpErr := net.Listen("tcp", address)
	if tcp != nil {
		_ = tcp.Close()
	}
	udp, udpErr := net.ListenPacket("udp", address)
	if udp != nil {
		_ = udp.Close()
	}
	if tcpErr == nil && udpErr == nil {
		return nil
	}
	return dnsPortDiagnostic(tcpErr, udpErr)
}

func dnsPortDiagnostic(tcpErr, udpErr error) *Issue {
	var detail string
	switch {
	case tcpErr != nil && udpErr != nil:
		detail = fmt.Sprintf("TCP: %v; UDP: %v", tcpErr, udpErr)
	case tcpErr != nil:
		detail = "TCP: " + tcpErr.Error()
	default:
		detail = "UDP: " + udpErr.Error()
	}
	return &Issue{
		Code:     "dns_port_unavailable",
		Severity: SeverityWarning,
		Message: "Windows DNS port 127.0.0.1:53 is unavailable; another TUN/VPN " +
			"client or an excluded port range may prevent cluster DNS from starting (" +
			detail + ")",
		Target: "127.0.0.1:53",
	}
}
