package utils

import (
	"fmt"
	"net"
	"strconv"
)

const (
	networkTCP = "tcp"
	networkUDP = "udp"

	loopbackHost = "127.0.0.1"
	// tcpUDPAttempts bounds the search for a port number that is free on both
	// transports, so a busy host fails with a diagnosable error instead of
	// looping forever.
	tcpUDPAttempts = 100
)

// FreeTCPPort reserves an ephemeral loopback TCP port and releases it again.
// The port is only guaranteed free until the caller binds it.
func FreeTCPPort() (int, error) {
	listener, err := net.Listen(networkTCP, net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected TCP listener address %T", listener.Addr())
	}
	return address.Port, nil
}

// FreeUDPPort reserves an ephemeral loopback UDP port and releases it again.
func FreeUDPPort() (int, error) {
	connection, err := net.ListenPacket(networkUDP, net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		return 0, err
	}
	defer func() { _ = connection.Close() }()
	address, ok := connection.LocalAddr().(*net.UDPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected UDP listener address %T", connection.LocalAddr())
	}
	return address.Port, nil
}

// FreeTCPUDPPort returns a port number that is free on both TCP and UDP, which
// listeners such as DNS need to share a single port across both transports.
func FreeTCPUDPPort() (int, error) {
	var lastErr error
	for range tcpUDPAttempts {
		udpListener, err := net.ListenPacket(networkUDP, net.JoinHostPort(loopbackHost, "0"))
		if err != nil {
			return 0, err
		}
		udpAddress, ok := udpListener.LocalAddr().(*net.UDPAddr)
		if !ok {
			_ = udpListener.Close()
			return 0, fmt.Errorf("unexpected UDP listener address %T", udpListener.LocalAddr())
		}
		port := udpAddress.Port
		tcpListener, tcpErr := net.Listen(
			networkTCP,
			net.JoinHostPort(loopbackHost, strconv.Itoa(port)),
		)
		_ = udpListener.Close()
		if tcpErr == nil {
			_ = tcpListener.Close()
			return port, nil
		}
		lastErr = tcpErr
	}
	return 0, fmt.Errorf("find TCP/UDP port: %w", lastErr)
}

// FreeTCPPortExcluding reserves a TCP port that is none of the excluded ones,
// so callers can hand out several distinct ports in a row.
func FreeTCPPortExcluding(excluded ...int) (int, error) {
	seen := make(map[int]struct{}, len(excluded))
	for _, port := range excluded {
		seen[port] = struct{}{}
	}
	for {
		port, err := FreeTCPPort()
		if err != nil {
			return 0, err
		}
		if _, exists := seen[port]; exists {
			continue
		}
		return port, nil
	}
}
