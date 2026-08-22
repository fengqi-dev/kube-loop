package traffic

import (
	"context"
	"fmt"
	"net"
)

const (
	socksVersion = 5

	socksMethodNone     = 0
	socksMethodPassword = 2

	socksCommandConnect      = 1
	socksCommandUDPAssociate = 3

	socksAddressIPv4   = 1
	socksAddressDomain = 3
	socksAddressIPv6   = 4
)

// Endpoint describes one loopback sing-box SOCKS inbound.
type Endpoint struct {
	Address  string
	Username string
	Password string
}

// Dialer opens fixed-destination TCP and UDP connections through a SOCKS5
// endpoint. The returned UDP net.Conn hides SOCKS datagram framing from callers.
type Dialer struct {
	Endpoint Endpoint
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return d.dialTCP(ctx, address)
	case "udp", "udp4", "udp6":
		return d.dialUDP(ctx, network, address)
	default:
		return nil, fmt.Errorf("unsupported traffic network %q", network)
	}
}
