package runtime

import (
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func TestDNSSearchProxyUDPAndTCP(t *testing.T) {
	upstreamAddr := startTestDNSUpstream(t)

	upstreamPort := upstreamAddr.Port
	proxy, publicPort := startTestDNSSearchProxy(
		t, upstreamPort,
		singbox.SearchDomains("default"), "cluster.local",
	)
	defer func() { _ = proxy.Close() }()

	req := new(dns.Msg)
	req.SetQuestion("kubernetes.default.svc.cluster.local.", dns.TypeA)
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort))

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

func TestDNSSearchProxyConcurrentCloseWaitsForShutdown(t *testing.T) {
	proxy := &dnsSearchProxy{}
	proxy.serveWG.Add(1)
	first := make(chan error, 1)
	go func() { first <- proxy.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		proxy.mu.Lock()
		closing := proxy.closed
		proxy.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first Close did not start")
		}
		time.Sleep(time.Millisecond)
	}
	second := make(chan error, 1)
	go func() { second <- proxy.Close() }()
	select {
	case err := <-second:
		t.Fatalf("concurrent Close returned before shutdown completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	proxy.serveWG.Done()
	for index, result := range []<-chan error{first, second} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Close %d: %v", index+1, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Close %d did not complete", index+1)
		}
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

	proxy, publicPort := startTestDNSSearchProxy(
		t, upstreamAddr.Port,
		singbox.SearchDomains("default"), "cluster.local",
	)
	defer func() { _ = proxy.Close() }()

	req := new(dns.Msg)
	req.SetQuestion("echo.", dns.TypeA)
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort))
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

func TestDNSSearchProxyStopsAfterExpandedNameReturnsNoData(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		mu.Lock()
		queries = append(queries, r.Question[0].Name)
		mu.Unlock()

		msg := new(dns.Msg)
		msg.SetReply(r)
		msg.Ns = []dns.RR{&dns.SOA{
			Hdr: dns.RR_Header{
				Name: "cluster.local.", Rrtype: dns.TypeSOA,
				Class: dns.ClassINET, Ttl: 30,
			},
			Ns: "ns.dns.cluster.local.", Mbox: "hostmaster.cluster.local.",
		}}
		_ = w.WriteMsg(msg)
	})
	upstreamAddr := startTestDNSUpstreamWithHandler(t, handler)

	proxy, publicPort := startTestDNSSearchProxy(
		t, upstreamAddr.Port,
		singbox.SearchDomains("default"), "cluster.local",
	)
	defer func() { _ = proxy.Close() }()

	req := new(dns.Msg)
	req.SetQuestion("echo.", dns.TypeAAAA)
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort))
	resp, _, err := (&dns.Client{Net: "udp", Timeout: 2 * time.Second}).Exchange(req, target)
	if err != nil {
		t.Fatalf("AAAA query: %v", err)
	}
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 {
		t.Fatalf("AAAA response = %#v, want NODATA", resp)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"echo.default.svc.cluster.local."}
	if !slices.Equal(queries, want) {
		t.Fatalf("upstream queries = %v, want %v", queries, want)
	}
}

func TestDNSSearchProxyUpdatesHostAliases(t *testing.T) {
	upstreamAddr := startTestDNSUpstream(t)
	proxy, publicPort := startTestDNSSearchProxy(
		t, upstreamAddr.Port,
		singbox.SearchDomains("default"), "cluster.local",
	)
	defer func() { _ = proxy.Close() }()

	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(publicPort))
	query := func() *dns.Msg {
		t.Helper()
		req := new(dns.Msg)
		req.SetQuestion("api.kubeloop.test.", dns.TypeA)
		resp, _, queryErr := (&dns.Client{
			Net: "udp", Timeout: 2 * time.Second,
		}).Exchange(req, target)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		return resp
	}
	assertIP := func(want string) {
		t.Helper()
		resp := query()
		if len(resp.Answer) != 1 {
			t.Fatalf("answer count = %d, want 1: %#v", len(resp.Answer), resp)
		}
		answer, ok := resp.Answer[0].(*dns.A)
		if !ok || answer.A.String() != want {
			t.Fatalf("answer = %#v, want %s", resp.Answer, want)
		}
	}

	proxy.SetHostAliases([]singbox.HostAlias{{
		Domain: "api.kubeloop.test", IP: "192.0.2.10",
	}})
	assertIP("192.0.2.10")
	proxy.SetHostAliases([]singbox.HostAlias{{
		Domain: "api.kubeloop.test", IP: "192.0.2.11",
	}})
	assertIP("192.0.2.11")
	proxy.SetHostAliases(nil)
	if resp := query(); resp.Rcode != dns.RcodeNameError {
		t.Fatalf("cleared alias rcode = %d, want NXDOMAIN", resp.Rcode)
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

func startTestDNSSearchProxy(
	t *testing.T,
	upstreamPort int,
	search []string,
	clusterDomains ...string,
) (*dnsSearchProxy, int) {
	t.Helper()
	for range 20 {
		reservation, err := net.Listen("tcp", net.JoinHostPort(singbox.DefaultDNSListen, "0"))
		if err != nil {
			t.Fatal(err)
		}
		publicPort := reservation.Addr().(*net.TCPAddr).Port
		_ = reservation.Close()
		proxy, err := startDNSSearchProxy(
			singbox.DefaultDNSListen,
			publicPort,
			singbox.DefaultDNSListen,
			upstreamPort,
			search,
			clusterDomains...,
		)
		if err == nil {
			return proxy, publicPort
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatal(err)
		}
	}
	t.Fatal("could not reserve a shared TCP/UDP DNS test port")
	return nil, 0
}

func startTestDNSUpstreamWithHandler(t *testing.T, handler dns.Handler) *net.UDPAddr {
	t.Helper()
	var (
		pc          net.PacketConn
		tcpListener net.Listener
		addr        *net.UDPAddr
	)
	for range 20 {
		var err error
		pc, err = net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr = pc.LocalAddr().(*net.UDPAddr)
		tcpListener, err = net.Listen("tcp", addr.String())
		if err == nil {
			break
		}
		_ = pc.Close()
		pc = nil
	}
	if pc == nil || tcpListener == nil {
		t.Fatal("could not reserve a shared TCP/UDP DNS upstream port")
	}
	udpServer := &dns.Server{PacketConn: pc, Handler: handler}
	go func() { _ = udpServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = udpServer.Shutdown() })

	tcpServer := &dns.Server{
		Listener: tcpListener, Handler: handler,
	}
	go func() { _ = tcpServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = tcpServer.Shutdown() })
	return addr
}
