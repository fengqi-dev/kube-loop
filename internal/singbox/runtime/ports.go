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
	type result struct {
		resp *dns.Msg
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, _, err := client.Exchange(msg, addr)
		ch <- result{resp: resp, err: err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case out := <-ch:
		if out.err != nil {
			return out.err
		}
		if out.resp == nil {
			return errors.New("empty DNS response")
		}
		if out.resp.Rcode != dns.RcodeSuccess || len(out.resp.Answer) == 0 {
			return fmt.Errorf("DNS lookup %s failed (rcode=%d)", qname, out.resp.Rcode)
		}
		return nil
	}
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

func availableTrafficPorts(excluded ...int) (singbox.TrafficInboundPorts, error) {
	seen := make(map[int]struct{}, len(excluded))
	for _, port := range excluded {
		seen[port] = struct{}{}
	}
	for {
		port, err := availablePort()
		if err != nil {
			return singbox.TrafficInboundPorts{}, err
		}
		if _, exists := seen[port]; exists {
			continue
		}
		return singbox.TrafficInboundPorts{Listen: port}, nil
	}
}

func trafficEndpoints(ports singbox.TrafficInboundPorts, password string) singbox.TrafficEndpoints {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.Listen))
	endpoint := func(username string) singbox.TrafficEndpoint {
		return singbox.TrafficEndpoint{
			Address:  address,
			Username: username,
			Password: password,
		}
	}
	return singbox.TrafficEndpoints{
		PortForward:  endpoint(singbox.TrafficUserPortForward),
		Exchange:     endpoint(singbox.TrafficUserExchange),
		Preview:      endpoint(singbox.TrafficUserPreview),
		MirrorShadow: endpoint(singbox.TrafficUserMirrorShadow),
	}
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
