package singbox

import (
	"context"
	"io"
	"time"
)

// PrivilegedStartFunc starts sing-box via an external privileged helper.
// It returns a stop function used during Close.
type PrivilegedStartFunc func(
	ctx context.Context, spec SessionSpec,
) (stop func(context.Context) error, err error)

// PrivilegedUpdateDNSFunc re-applies split DNS without restarting sing-box.
type PrivilegedUpdateDNSFunc func(
	ctx context.Context, sessionID string, dns DNSMeta,
) error

type PrivilegedReadLogsFunc func(
	ctx context.Context, sessionID string, offset int64,
) (data string, nextOffset int64, err error)

// RunningCore is the session-facing contract implemented by the runtime
// subpackage. Keeping it with the configuration model prevents orchestration
// packages from depending on process-management details.
type RunningCore interface {
	io.Closer
	Done() <-chan struct{}
	Err() error
	SessionID() string
	Config() []byte
	ReadLogs(ctx context.Context) ([]string, error)
	UpdateDNSNamespace(ctx context.Context, namespace string) error
	UpdateHostAliases(ctx context.Context, hosts []HostAlias) error
	ProbeClusterDNS(ctx context.Context) error
	DNSPort() int
	InternalDNSPort() int
}

const DefaultMetricsInterval = time.Second
