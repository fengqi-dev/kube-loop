package singbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
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

// RunningCore is the session-facing contract implemented by the runtime
// subpackage. Keeping it with the configuration model prevents orchestration
// packages from depending on process-management details.
type RunningCore interface {
	io.Closer
	Done() <-chan struct{}
	Err() error
	Snapshot(ctx context.Context) (Metrics, error)
	TrafficEndpoints() TrafficEndpoints
	Config() []byte
	UpdateDNSNamespace(ctx context.Context, namespace string) error
	ProbeClusterDNS(ctx context.Context) error
	DNSPort() int
	InternalDNSPort() int
}

type TrafficEndpoint struct {
	Address  string
	Username string
	Password string
}

type TrafficEndpoints struct {
	PortForward  TrafficEndpoint
	Exchange     TrafficEndpoint
	Preview      TrafficEndpoint
	MirrorShadow TrafficEndpoint
}

func (e TrafficEndpoints) Validate() error {
	items := []struct {
		name     string
		username string
		endpoint TrafficEndpoint
	}{
		{TrafficUserPortForward, TrafficUserPortForward, e.PortForward},
		{TrafficUserExchange, TrafficUserExchange, e.Exchange},
		{TrafficUserPreview, TrafficUserPreview, e.Preview},
		{TrafficUserMirrorShadow, TrafficUserMirrorShadow, e.MirrorShadow},
	}
	var sharedAddress, sharedPassword string
	for i, item := range items {
		host, rawPort, err := net.SplitHostPort(item.endpoint.Address)
		if err != nil {
			return fmt.Errorf("%s address: %w", item.name, err)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("%s must listen on loopback", item.name)
		}
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("%s has invalid port", item.name)
		}
		if item.endpoint.Username != item.username {
			return fmt.Errorf("%s username must be %q", item.name, item.username)
		}
		if item.endpoint.Password == "" {
			return fmt.Errorf("%s password is required", item.name)
		}
		if i == 0 {
			sharedAddress = item.endpoint.Address
			sharedPassword = item.endpoint.Password
			continue
		}
		if item.endpoint.Address != sharedAddress {
			return errors.New("traffic endpoints must share one listen address")
		}
		if item.endpoint.Password != sharedPassword {
			return errors.New("traffic endpoints must share one password")
		}
	}
	return nil
}

const DefaultMetricsInterval = time.Second
