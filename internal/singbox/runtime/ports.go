package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/miekg/dns"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/utils"
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

func availableTrafficPorts(excluded ...int) (sessionspec.TrafficInboundPorts, error) {
	port, err := utils.FreeTCPPortExcluding(excluded...)
	if err != nil {
		return sessionspec.TrafficInboundPorts{}, err
	}
	return sessionspec.TrafficInboundPorts{Listen: port}, nil
}
