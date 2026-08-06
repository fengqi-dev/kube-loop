package runtime

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/miekg/dns"
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
	clientUDP *dns.Client
	clientTCP *dns.Client

	mu     sync.Mutex
	closed bool
}

func startDNSSearchProxy(
	publicHost string, publicPort int, upstreamHost string, upstreamPort int,
	search []string, clusterDomains ...string,
) (*dnsSearchProxy, error) {
	if publicHost == "" {
		publicHost = singbox.DefaultDNSListen
	}
	if upstreamHost == "" {
		upstreamHost = singbox.DefaultDNSListen
	}
	domains, err := dnsname.NormalizeClusterDomains(clusterDomains)
	if err != nil {
		domains = []string{dnsname.DefaultClusterDomain}
	}
	proxy := &dnsSearchProxy{
		upstream:  net.JoinHostPort(upstreamHost, fmt.Sprintf("%d", upstreamPort)),
		search:    slices.Clone(search),
		domains:   domains,
		clientUDP: &dns.Client{Net: "udp", Timeout: 3 * time.Second, UDPSize: 1232},
		clientTCP: &dns.Client{Net: "tcp", Timeout: 5 * time.Second},
	}
	addr := net.JoinHostPort(publicHost, fmt.Sprintf("%d", publicPort))
	handler := dns.HandlerFunc(proxy.serveDNS)
	proxy.publicUDP = &dns.Server{Addr: addr, Net: "udp", Handler: handler, UDPSize: 1232}
	proxy.publicTCP = &dns.Server{Addr: addr, Net: "tcp", Handler: handler}

	errCh := make(chan error, 2)
	go func() { errCh <- proxy.publicUDP.ListenAndServe() }()
	go func() { errCh <- proxy.publicTCP.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != nil {
			_ = proxy.Close()
			return nil, fmt.Errorf("listen DNS search proxy on %s: %w", addr, err)
		}
	case <-time.After(150 * time.Millisecond):
	}
	return proxy, nil
}

func (p *dnsSearchProxy) SetSearch(search []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.search = slices.Clone(search)
}

func (p *dnsSearchProxy) SetClusterDomains(domains []string) {
	normalized, err := dnsname.NormalizeClusterDomains(domains)
	if err != nil {
		normalized = []string{dnsname.DefaultClusterDomain}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.domains = normalized
}

func (p *dnsSearchProxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var first error
	if p.publicUDP != nil {
		if err := p.publicUDP.Shutdown(); err != nil && first == nil {
			first = err
		}
	}
	if p.publicTCP != nil {
		if err := p.publicTCP.Shutdown(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (p *dnsSearchProxy) serveDNS(w dns.ResponseWriter, req *dns.Msg) {
	if req == nil || len(req.Question) == 0 {
		_ = w.WriteMsg(new(dns.Msg).SetRcode(req, dns.RcodeFormatError))
		return
	}
	p.mu.Lock()
	search := slices.Clone(p.search)
	domains := slices.Clone(p.domains)
	p.mu.Unlock()

	original := req.Question[0].Name
	candidates := dnsSearchCandidates(original, search, domains...)
	network := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		network = "tcp"
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
		if len(resp.Answer) == 0 && !equalDNSName(candidate, original) {
			// NODATA on an expanded name — keep trying other suffixes for A/AAAA.
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
	if network == "tcp" {
		client = p.clientTCP
	}
	resp, _, err := client.Exchange(req, p.upstream)
	if err == nil && resp != nil && resp.Truncated && network == "udp" {
		resp, _, err = p.clientTCP.Exchange(req, p.upstream)
	}
	return resp, err
}

func dnsSearchCandidates(qname string, search []string, clusterDomains ...string) []string {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(qname)), ".")
	if name == "" {
		return nil
	}
	original := name + "."
	domains, err := dnsname.NormalizeClusterDomains(clusterDomains)
	if err != nil || len(domains) == 0 {
		domains = []string{dnsname.DefaultClusterDomain}
	}
	for _, domain := range domains {
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return []string{original}
		}
	}
	if strings.HasSuffix(name, ".in-addr.arpa") || strings.HasSuffix(name, ".ip6.arpa") {
		return []string{original}
	}
	out := make([]string, 0, len(search)+1)
	seen := make(map[string]struct{}, len(search)+1)
	for _, suffix := range search {
		suffix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), ".")
		if suffix == "" {
			continue
		}
		candidate := name + "." + suffix + "."
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	if _, ok := seen[original]; !ok {
		out = append(out, original)
	}
	return out
}

func rewriteDNSNames(msg *dns.Msg, from, to string) {
	if msg == nil || from == "" || to == "" || equalDNSName(from, to) {
		return
	}
	for i := range msg.Question {
		if equalDNSName(msg.Question[i].Name, from) {
			msg.Question[i].Name = to
		}
	}
	rewriteRRNames(msg.Answer, from, to)
	rewriteRRNames(msg.Ns, from, to)
	rewriteRRNames(msg.Extra, from, to)
}

func rewriteRRNames(records []dns.RR, from, to string) {
	for _, rr := range records {
		if rr == nil {
			continue
		}
		hdr := rr.Header()
		if equalDNSName(hdr.Name, from) {
			hdr.Name = to
		}
	}
}

func equalDNSName(left, right string) bool {
	return strings.EqualFold(strings.TrimSuffix(left, "."), strings.TrimSuffix(right, "."))
}
