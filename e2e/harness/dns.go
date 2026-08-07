//go:build e2e

package harness

import (
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// DNSServer is the address of the local search-proxy / split-DNS listener.
func DNSServer(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// WaitDNSA queries the local DNS proxy for an A record until wantIP appears.
func WaitDNSA(t *testing.T, port int, name, wantIP string) {
	t.Helper()
	waitDNS(t, port, "udp", name, dns.TypeA, func(msg *dns.Msg) bool {
		for _, rr := range msg.Answer {
			if a, ok := rr.(*dns.A); ok && a.A.String() == wantIP {
				return true
			}
		}
		return false
	}, fmt.Sprintf("A %s -> %s", name, wantIP))
}

// WaitDNSTCPA is WaitDNSA over TCP (exercises the search-proxy TCP path).
func WaitDNSTCPA(t *testing.T, port int, name, wantIP string) {
	t.Helper()
	waitDNS(t, port, "tcp", name, dns.TypeA, func(msg *dns.Msg) bool {
		for _, rr := range msg.Answer {
			if a, ok := rr.(*dns.A); ok && a.A.String() == wantIP {
				return true
			}
		}
		return false
	}, fmt.Sprintf("TCP A %s -> %s", name, wantIP))
}

// WaitDNSNXDOMAIN waits until the local DNS proxy returns NXDOMAIN for name.
func WaitDNSNXDOMAIN(t *testing.T, port int, name string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		msg, err := exchangeDNS(port, "udp", name, dns.TypeA)
		if err == nil && msg != nil && msg.Rcode == dns.RcodeNameError {
			return
		}
		if err != nil {
			lastErr = err
		} else if msg != nil {
			lastErr = fmt.Errorf("rcode=%d answers=%d", msg.Rcode, len(msg.Answer))
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf("DNS NXDOMAIN %s: %v", name, lastErr)
}

// WaitDNSNotA waits until name no longer resolves to wantIP.
func WaitDNSNotA(t *testing.T, port int, name, wantIP string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		msg, err := exchangeDNS(port, "udp", name, dns.TypeA)
		if err == nil && msg != nil {
			found := false
			for _, rr := range msg.Answer {
				if a, ok := rr.(*dns.A); ok && a.A.String() == wantIP {
					found = true
					break
				}
			}
			if !found {
				return
			}
			lastErr = fmt.Errorf("still resolves to %s", wantIP)
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("DNS A %s still resolves to %s: %v", name, wantIP, lastErr)
}

func waitDNS(
	t *testing.T,
	port int,
	network, name string,
	qtype uint16,
	ok func(*dns.Msg) bool,
	label string,
) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		msg, err := exchangeDNS(port, network, name, qtype)
		if err == nil && msg != nil && msg.Rcode == dns.RcodeSuccess && ok(msg) {
			return
		}
		if err != nil {
			lastErr = err
			last = ""
		} else if msg != nil {
			lastErr = fmt.Errorf("rcode=%d answers=%d", msg.Rcode, len(msg.Answer))
			last = msg.String()
		}
		time.Sleep(400 * time.Millisecond)
	}
	if last != "" {
		t.Fatalf("DNS %s: %v\n%s", label, lastErr, last)
	}
	t.Fatalf("DNS %s: %v", label, lastErr)
}

// ExchangeDNS sends a single DNS query to the local search proxy.
func ExchangeDNS(port int, network, name string, qtype uint16) (*dns.Msg, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), qtype)
	msg.RecursionDesired = true
	client := &dns.Client{Net: network, Timeout: 3 * time.Second}
	resp, _, err := client.Exchange(msg, DNSServer(port))
	return resp, err
}

func WaitDNSProxyGone(t *testing.T, port int, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ExchangeDNS(port, "udp", name, dns.TypeA); err != nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("DNS proxy on :%d still answers %s", port, name)
}

func exchangeDNS(port int, network, name string, qtype uint16) (*dns.Msg, error) {
	return ExchangeDNS(port, network, name, qtype)
}
