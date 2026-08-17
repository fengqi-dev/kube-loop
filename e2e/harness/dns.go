//go:build e2e

package harness

import (
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

// ExchangeDNS sends a single DNS query to the local search proxy.
func ExchangeDNS(port int, network, name string, qtype uint16) (*dns.Msg, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), qtype)
	msg.RecursionDesired = true
	client := &dns.Client{Net: network, Timeout: 3 * time.Second}
	response, _, err := client.Exchange(msg, DNSServer(port))
	return response, err
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
