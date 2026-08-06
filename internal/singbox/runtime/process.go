package runtime

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type Process struct {
	done              chan struct{}
	stopCh            chan struct{}
	helperStop        func(context.Context) error
	controllerAddress string
	controllerSecret  string
	dnsPort           int
	internalDNSPort   int
	resolverDomains   []string
	dnsProxy          *dnsSearchProxy
	httpClient        *http.Client
	closeOnce         sync.Once
	errMu             sync.RWMutex
	waitErr           error
	trafficEndpoints  singbox.TrafficEndpoints
	config            []byte
	spec              singbox.SessionSpec
	updateDNS         singbox.PrivilegedUpdateDNSFunc
	specMu            sync.Mutex
}

var _ singbox.RunningCore = (*Process)(nil)

func (p *Process) Done() <-chan struct{} { return p.done }

func (p *Process) Err() error {
	p.errMu.RLock()
	defer p.errMu.RUnlock()
	return p.waitErr
}

func (p *Process) TrafficEndpoints() singbox.TrafficEndpoints { return p.trafficEndpoints }

func (p *Process) DNSPort() int         { return p.dnsPort }
func (p *Process) InternalDNSPort() int { return p.internalDNSPort }

func (p *Process) Config() []byte {
	if len(p.config) == 0 {
		return nil
	}
	return slices.Clone(p.config)
}

func (p *Process) UpdateDNSNamespace(ctx context.Context, namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = "default"
	}
	p.specMu.Lock()
	nextSpec := p.spec
	nextSpec.DNSNamespace = namespace
	nextSpec.Namespace = namespace
	dns, err := nextSpec.DNS()
	domains := slices.Clone(nextSpec.ClusterDomains)
	sessionID := nextSpec.ID
	proxy := p.dnsProxy
	if err == nil {
		p.spec = nextSpec
		p.resolverDomains = dns.Domains
	}
	p.specMu.Unlock()
	if err != nil {
		return err
	}
	// Capture proxy under specMu so Close cannot nil it mid-update.
	if proxy != nil {
		proxy.SetSearch(dns.Search)
		proxy.SetClusterDomains(domains)
	}
	if p.updateDNS == nil {
		return errors.New("privileged DNS update is unavailable; reconnect to apply")
	}
	return p.updateDNS(ctx, sessionID, dns)
}

func (p *Process) ProbeClusterDNS(ctx context.Context) error {
	p.specMu.Lock()
	domains := slices.Clone(p.spec.ClusterDomains)
	port := p.dnsPort
	p.specMu.Unlock()
	if len(domains) == 0 {
		domains = []string{dnsname.DefaultClusterDomain}
	}
	name := "kubernetes.default.svc." + domains[0] + "."
	return probeLocalDNS(ctx, singbox.DefaultDNSListen, port, name)
}

func (p *Process) request(ctx context.Context, path string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+p.controllerAddress+path, nil,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+p.controllerSecret)
	client := p.httpClient
	if client == nil {
		client = &http.Client{Timeout: time.Second}
	}
	return client.Do(request)
}

func (p *Process) wait() {
	<-p.stopCh
	p.errMu.Lock()
	p.waitErr = nil
	p.errMu.Unlock()
	close(p.done)
}

func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		select {
		case <-p.done:
		default:
			// helperStop blocks until the helper has restored DNS and routes.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if p.helperStop != nil {
				_ = p.helperStop(ctx)
			}
			cancel()
			close(p.stopCh)
			select {
			case <-p.done:
			case <-time.After(20 * time.Second):
				select {
				case <-p.done:
				case <-time.After(2 * time.Second):
				}
			}
		}
		p.specMu.Lock()
		proxy := p.dnsProxy
		p.dnsProxy = nil
		p.specMu.Unlock()
		if proxy != nil {
			_ = proxy.Close()
		}
	})
	err := p.Err()
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "signal") {
		return err
	}
	return nil
}
