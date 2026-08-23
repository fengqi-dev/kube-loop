package runtime

import (
	"strings"

	"github.com/miekg/dns"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
)

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
	if name == "svc" || strings.HasSuffix(name, ".svc") {
		out := make([]string, 0, len(domains))
		for _, domain := range domains {
			out = append(out, name+"."+domain+".")
		}
		return out
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
