package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/miekg/dns"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func probeLocalDNS(ctx context.Context, host string, port int, qname string) error {
	if host == "" {
		host = singbox.DefaultDNSListen
	}
	if port < 1 {
		return errors.New("DNS port is unavailable")
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(qname), dns.TypeA)
	client := &dns.Client{Net: networkUDP, Timeout: 3 * time.Second}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	resp, _, err := client.ExchangeContext(ctx, msg, addr)
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("empty DNS response")
	}
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) == 0 {
		return fmt.Errorf("DNS lookup %s failed (rcode=%d)", qname, resp.Rcode)
	}
	return nil
}

func availablePort() (int, error) {
	listener, err := net.Listen(networkTCP, "127.0.0.1:0")
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

func availableTCPUDPPort() (int, error) {
	var lastErr error
	for range 100 {
		udpListener, err := net.ListenPacket(networkUDP, "127.0.0.1:0")
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
			net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		)
		if tcpErr == nil {
			_ = tcpListener.Close()
			_ = udpListener.Close()
			return port, nil
		}
		lastErr = tcpErr
		_ = udpListener.Close()
	}
	return 0, fmt.Errorf("find TCP/UDP port: %w", lastErr)
}

func availableTrafficPorts(excluded ...int) (sessionspec.TrafficInboundPorts, error) {
	port, err := availablePortExcluding(excluded...)
	if err != nil {
		return sessionspec.TrafficInboundPorts{}, err
	}
	return sessionspec.TrafficInboundPorts{Listen: port}, nil
}

func availablePortExcluding(excluded ...int) (int, error) {
	seen := make(map[int]struct{}, len(excluded))
	for _, port := range excluded {
		seen[port] = struct{}{}
	}
	for {
		port, err := availablePort()
		if err != nil {
			return 0, err
		}
		if _, exists := seen[port]; exists {
			continue
		}
		return port, nil
	}
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
