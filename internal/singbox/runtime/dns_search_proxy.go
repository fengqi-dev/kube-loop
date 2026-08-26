package runtime

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	dnsprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/dns"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const (
	defaultNamespace = "default"
	networkTCP       = "tcp"
	networkUDP       = "udp"
)

// dnsSearchProxy accepts OS DNS queries on the public split-DNS port, appends
// Kubernetes search suffixes when needed, and forwards to sing-box dns-in.
//
// On macOS, networksetup search domains expand the name and then query the
// primary resolver (e.g. 114.114.114.114), so short names never hit
// /etc/resolver/cluster.local. Matching *.svc via /etc/resolver/svc and
// expanding here makes names like static-web.default.svc work.
type dnsSearchProxy struct {
	publicUDP *dns.Server
	publicTCP *dns.Server
	upstream  string
	search    []string
	domains   []string
	hosts     map[string]net.IP
	clientUDP *dns.Client
	clientTCP *dns.Client

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
	serveWG   sync.WaitGroup
}

func startDNSSearchProxy(
	ctx context.Context,
	publicHost string, publicPort int, upstreamHost string, upstreamPort int,
	search []string, clusterDomains ...string,
) (*dnsSearchProxy, error) {
	if publicHost == "" {
		publicHost = singbox.DefaultDNSListen
	}
	if upstreamHost == "" {
		upstreamHost = singbox.DefaultDNSListen
	}
	domains, err := dnsprotocol.NormalizeClusterDomains(clusterDomains)
	if err != nil {
		domains = []string{dnsprotocol.DefaultClusterDomain}
	}
	proxy := &dnsSearchProxy{
		upstream:  net.JoinHostPort(upstreamHost, strconv.Itoa(upstreamPort)),
		search:    slices.Clone(search),
		domains:   domains,
		hosts:     make(map[string]net.IP),
		clientUDP: &dns.Client{Net: networkUDP, Timeout: 3 * time.Second, UDPSize: 1232},
		clientTCP: &dns.Client{Net: networkTCP, Timeout: 5 * time.Second},
	}
	addr := net.JoinHostPort(publicHost, strconv.Itoa(publicPort))
	handler := dns.HandlerFunc(proxy.serveDNS)
	var listenConfig net.ListenConfig
	packetConnection, err := listenConfig.ListenPacket(ctx, networkUDP, addr)
	if err != nil {
		return nil, fmt.Errorf("listen UDP DNS search proxy on %s: %w", addr, err)
	}
	tcpListener, err := listenConfig.Listen(ctx, networkTCP, addr)
	if err != nil {
		_ = packetConnection.Close()
		return nil, fmt.Errorf("listen TCP DNS search proxy on %s: %w", addr, err)
	}
	proxy.publicUDP = &dns.Server{
		Addr: addr, Net: networkUDP, Handler: handler, UDPSize: 1232,
		PacketConn: packetConnection,
	}
	proxy.publicTCP = &dns.Server{
		Addr: addr, Net: networkTCP, Handler: handler,
		Listener: tcpListener,
	}

	started := make(chan struct{}, 2)
	proxy.publicUDP.NotifyStartedFunc = func() { started <- struct{}{} }
	proxy.publicTCP.NotifyStartedFunc = func() { started <- struct{}{} }
	errCh := make(chan error, 2)
	proxy.serveWG.Add(2)
	go func() {
		defer proxy.serveWG.Done()
		errCh <- proxy.publicUDP.ActivateAndServe()
	}()
	go func() {
		defer proxy.serveWG.Done()
		errCh <- proxy.publicTCP.ActivateAndServe()
	}()
	for range 2 {
		select {
		case <-started:
		case serveErr := <-errCh:
			_ = packetConnection.Close()
			_ = tcpListener.Close()
			proxy.serveWG.Wait()
			return nil, fmt.Errorf("activate DNS search proxy on %s: %w", addr, serveErr)
		}
	}
	return proxy, nil
}

func (p *dnsSearchProxy) SetSearch(search []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.search = slices.Clone(search)
}

func (p *dnsSearchProxy) SetClusterDomains(domains []string) {
	normalized, err := dnsprotocol.NormalizeClusterDomains(domains)
	if err != nil {
		normalized = []string{dnsprotocol.DefaultClusterDomain}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.domains = normalized
}

func (p *dnsSearchProxy) SetHostAliases(hosts []singbox.HostAlias) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hosts == nil {
		p.hosts = make(map[string]net.IP)
	}
	for name := range p.hosts {
		// Keep a tombstone for aliases embedded in the running sing-box config,
		// so a removed alias cannot leak through the upstream until reconnect.
		p.hosts[name] = nil
	}
	for _, host := range hosts {
		name := strings.ToLower(dns.Fqdn(host.Domain))
		if ip := net.ParseIP(host.IP).To4(); ip != nil {
			p.hosts[name] = slices.Clone(ip)
		}
	}
}

func (p *dnsSearchProxy) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		publicUDP, publicTCP := p.publicUDP, p.publicTCP
		p.mu.Unlock()
		if publicUDP != nil {
			if err := publicUDP.Shutdown(); err != nil {
				p.closeErr = err
			}
		}
		if publicTCP != nil {
			if err := publicTCP.Shutdown(); err != nil && p.closeErr == nil {
				p.closeErr = err
			}
		}
		p.serveWG.Wait()
	})
	return p.closeErr
}

func (p *dnsSearchProxy) serveDNS(w dns.ResponseWriter, req *dns.Msg) {
	if req == nil || len(req.Question) == 0 {
		_ = w.WriteMsg(new(dns.Msg).SetRcode(req, dns.RcodeFormatError))
		return
	}
	p.mu.Lock()
	search := slices.Clone(p.search)
	domains := slices.Clone(p.domains)
	hostIP, managedHost := p.hosts[strings.ToLower(req.Question[0].Name)]
	hostIP = slices.Clone(hostIP)
	p.mu.Unlock()

	original := req.Question[0].Name
	if managedHost {
		out := new(dns.Msg)
		out.SetReply(req)
		if req.Question[0].Qtype == dns.TypeA && hostIP != nil {
			out.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{
					Name: original, Rrtype: dns.TypeA,
					Class: dns.ClassINET, Ttl: 1,
				},
				A: hostIP,
			}}
		} else if hostIP == nil {
			out.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(out)
		return
	}
	candidates := dnsSearchCandidates(original, search, domains...)
	network := networkUDP
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		network = networkTCP
	}
	var last *dns.Msg
	for _, candidate := range candidates {
		forward := req.Copy()
		forward.Id = dns.Id()
		forward.Question[0].Name = candidate
		resp, err := p.exchange(network, forward)
		if err != nil || resp == nil {
			continue
		}
		last = resp
		if resp.Rcode != dns.RcodeSuccess {
			continue
		}
		out := resp.Copy()
		out.Id = req.Id
		rewriteDNSNames(out, candidate, original)
		_ = w.WriteMsg(out)
		return
	}
	if last != nil {
		out := last.Copy()
		out.Id = req.Id
		rewriteDNSNames(out, last.Question[0].Name, original)
		_ = w.WriteMsg(out)
		return
	}
	nx := new(dns.Msg)
	nx.SetReply(req)
	nx.Rcode = dns.RcodeServerFailure
	_ = w.WriteMsg(nx)
}

func (p *dnsSearchProxy) exchange(network string, req *dns.Msg) (*dns.Msg, error) {
	client := p.clientUDP
	if network == networkTCP {
		client = p.clientTCP
	}
	resp, _, err := client.Exchange(req, p.upstream)
	if err == nil && resp != nil && resp.Truncated && network == networkUDP {
		resp, _, err = p.clientTCP.Exchange(req, p.upstream)
	}
	return resp, err
}
