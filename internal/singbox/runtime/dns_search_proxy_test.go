package runtime

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/miekg/dns"
)

func TestDNSSearchProxyUDPAndTCP(t *testing.T) {
	upstreamAddr := startTestDNSUpstream(t)

	publicTCP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	publicPort := publicTCP.Addr().(*net.TCPAddr).Port
	_ = publicTCP.Close()

	host, upstreamHost, upstreamPort := "127.0.0.1", "127.0.0.1", upstreamAddr.Port
	proxy, err := startDNSSearchProxy(
		host, publicPort, upstreamHost, upstreamPort,
		singbox.SearchDomains("default"), "cluster.local",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Close() }()

	req := new(dns.Msg)
	req.SetQuestion("kubernetes.default.svc.cluster.local.", dns.TypeA)
	target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", publicPort))

	udpClient := &dns.Client{Net: "udp", Timeout: 2 * time.Second}
	resp, _, err := udpClient.Exchange(req, target)
	if err != nil {
		t.Fatalf("udp query: %v", err)
	}
	if len(resp.Answer) == 0 {
		t.Fatalf("udp empty answer: %#v", resp)
	}

	tcpClient := &dns.Client{Net: "tcp", Timeout: 2 * time.Second}
	resp, _, err = tcpClient.Exchange(req, target)
	if err != nil {
		t.Fatalf("tcp query: %v", err)
	}
	if len(resp.Answer) == 0 {
		t.Fatalf("tcp empty answer: %#v", resp)
	}
}

func TestDNSSearchProxyPrefersExpandedClusterName(t *testing.T) {
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		msg := new(dns.Msg)
		msg.SetReply(r)
		name := r.Question[0].Name
		switch name {
		case "echo.default.svc.cluster.local.":
			msg.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{
					Name: name, Rrtype: dns.TypeA,
					Class: dns.ClassINET, Ttl: 30,
				},
				A: net.ParseIP("10.96.0.20").To4(),
			}}
		default:
			if strings.HasSuffix(name, ".cluster.local.") {
				msg.Rcode = dns.RcodeNameError
			} else {
				// Simulate sing-box fake-IP behavior for unresolved public names.
				msg.Answer = []dns.RR{&dns.A{
					Hdr: dns.RR_Header{
						Name: name, Rrtype: dns.TypeA,
						Class: dns.ClassINET, Ttl: 30,
					},
					A: net.ParseIP("198.18.1.1").To4(),
				}}
			}
		}
		_ = w.WriteMsg(msg)
	})
	upstreamAddr := startTestDNSUpstreamWithHandler(t, handler)

	publicTCP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	publicPort := publicTCP.Addr().(*net.TCPAddr).Port
	_ = publicTCP.Close()

	proxy, err := startDNSSearchProxy(
		"127.0.0.1", publicPort, "127.0.0.1", upstreamAddr.Port,
		singbox.SearchDomains("default"), "cluster.local",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Close() }()

	req := new(dns.Msg)
	req.SetQuestion("echo.", dns.TypeA)
	target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", publicPort))
	resp, _, err := (&dns.Client{Net: "udp", Timeout: 2 * time.Second}).Exchange(req, target)
	if err != nil {
		t.Fatalf("short-name query: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("short-name answer count = %d, want 1: %#v", len(resp.Answer), resp)
	}
	answer, ok := resp.Answer[0].(*dns.A)
	if !ok || answer.A.String() != "10.96.0.20" {
		t.Fatalf("short-name answer = %v, want cluster IP 10.96.0.20", resp.Answer)
	}
	if answer.Hdr.Name != "echo." {
		t.Fatalf("rewritten answer name = %q, want echo.", answer.Hdr.Name)
	}
}

func TestDNSSearchCandidates(t *testing.T) {
	got := dnsSearchCandidates(
		"static-web.default.svc.",
		singbox.SearchDomains("default"),
	)
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "static-web.default.svc.cluster.local.") {
		t.Fatalf("missing FQDN candidate: %v", got)
	}
	if got[0] != "static-web.default.svc.default.svc.cluster.local." {
		t.Fatalf("Kubernetes search expansion should be first: %v", got)
	}
	if got[len(got)-1] != "static-web.default.svc." {
		t.Fatalf("original name should be the final fallback: %v", got)
	}
	fqdn := dnsSearchCandidates(
		"api.default.svc.cluster.local.",
		singbox.SearchDomains("default"),
	)
	if len(fqdn) != 1 || fqdn[0] != "api.default.svc.cluster.local." {
		t.Fatalf("FQDN should not expand: %v", fqdn)
	}
}

func startTestDNSUpstream(t *testing.T) *net.UDPAddr {
	t.Helper()
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		msg := new(dns.Msg)
		msg.SetReply(r)
		msg.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{
					Name: r.Question[0].Name, Rrtype: dns.TypeA,
					Class: dns.ClassINET, Ttl: 30,
				},
				A: net.ParseIP("10.96.0.1").To4(),
			},
		}
		_ = w.WriteMsg(msg)
	})
	return startTestDNSUpstreamWithHandler(t, handler)
}

func startTestDNSUpstreamWithHandler(t *testing.T, handler dns.Handler) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().(*net.UDPAddr)
	udpServer := &dns.Server{PacketConn: pc, Handler: handler}
	go func() { _ = udpServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = udpServer.Shutdown() })

	tcpServer := &dns.Server{
		Addr: addr.String(), Net: "tcp", Handler: handler,
	}
	go func() { _ = tcpServer.ListenAndServe() }()
	t.Cleanup(func() { _ = tcpServer.Shutdown() })
	time.Sleep(30 * time.Millisecond)
	return addr
}
