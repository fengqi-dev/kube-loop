package runtime

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const maxDataPlaneLogLines = 5_000

const helperStopTimeout = 20 * time.Second

var errProcessClosed = errors.New("sing-box process is closed")

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
	config            []byte
	spec              singbox.SessionSpec
	updateDNS         singbox.PrivilegedUpdateDNSFunc
	readLogs          singbox.PrivilegedReadLogsFunc
	updateMu          sync.Mutex
	specMu            sync.Mutex
	closed            bool
	logMu             sync.Mutex
	logOffset         int64
	logPending        string
	logHistory        []string
}

var _ singbox.RunningCore = (*Process)(nil)

func (p *Process) Done() <-chan struct{} { return p.done }

func (p *Process) Err() error {
	p.errMu.RLock()
	defer p.errMu.RUnlock()
	return p.waitErr
}

func (p *Process) SessionID() string {
	p.specMu.Lock()
	defer p.specMu.Unlock()
	return p.spec.ID
}

func (p *Process) DNSPort() int         { return p.dnsPort }
func (p *Process) InternalDNSPort() int { return p.internalDNSPort }

func (p *Process) Config() []byte {
	if len(p.config) == 0 {
		return nil
	}
	return slices.Clone(p.config)
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
