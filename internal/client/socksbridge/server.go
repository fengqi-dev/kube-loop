package socksbridge

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

// HostTCPHandler claims intercepted Service destinations on the host TUN path.
// When ok is true, the bridge writes the SOCKS success reply then calls serve.
type HostTCPHandler func(host string, port uint16) (serve func(net.Conn), ok bool)

// HostUDPHandler claims intercepted UDP destinations on the host TUN path.
// dial opens a connection that exchanges raw datagram payloads via Read/Write
// (not tunnel length-prefix framing).
type HostUDPHandler func(host string, port uint16) (dial func(context.Context) (net.Conn, error), ok bool)

type LogHandler func(message string)

type Server struct {
	GatewayAddress string
	SessionToken   tunnel.SessionToken
	DialTimeout    time.Duration
	HostTCP        HostTCPHandler
	HostUDP        HostUDPHandler
	LogHandler     LogHandler

	gatewayMu sync.RWMutex
	hostMu    sync.RWMutex
	logMu     sync.RWMutex
	tasks     *goroutinePool
}

// Bridge is the local SOCKS listener used by sing-box's kubernetes outbound.
type Bridge struct {
	net.Listener

	server      *Server
	serveDone   <-chan struct{}
	stopContext func() bool
	closeOnce   sync.Once
	closeErr    error
}

// Close stops the SOCKS listener and waits for active handlers.
func (b *Bridge) Close() error {
	b.closeOnce.Do(func() {
		if b.stopContext != nil {
			b.stopContext()
		}
		listenerErr := b.Listener.Close()
		if errors.Is(listenerErr, net.ErrClosed) {
			listenerErr = nil
		}
		b.closeErr = listenerErr
		if b.serveDone != nil {
			<-b.serveDone
		}
		if b.server.tasks != nil {
			b.server.tasks.Wait()
		}
	})
	return b.closeErr
}

func (b *Bridge) SetHostTCPHandler(handler HostTCPHandler) {
	b.server.hostMu.Lock()
	defer b.server.hostMu.Unlock()
	b.server.HostTCP = handler
}

func (b *Bridge) SetHostUDPHandler(handler HostUDPHandler) {
	b.server.hostMu.Lock()
	defer b.server.hostMu.Unlock()
	b.server.HostUDP = handler
}

func (b *Bridge) SetLogHandler(handler LogHandler) {
	b.server.logMu.Lock()
	b.server.LogHandler = handler
	b.server.logMu.Unlock()
}

// SetGatewayAddress switches new SOCKS requests to a replacement Kubernetes
// API port-forward without interrupting the local sing-box listener.
func (b *Bridge) SetGatewayAddress(address string) {
	b.server.gatewayMu.Lock()
	b.server.GatewayAddress = address
	b.server.gatewayMu.Unlock()
}

// SetGateway atomically switches the endpoint and its generation-bound
// protocol tenant token. Existing streams keep their established connection;
// new streams use the replacement generation.
func (b *Bridge) SetGateway(address string, token tunnel.SessionToken) {
	b.server.gatewayMu.Lock()
	b.server.GatewayAddress = address
	b.server.SessionToken = token
	b.server.gatewayMu.Unlock()
}
