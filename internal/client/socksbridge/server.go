package socksbridge

import (
	"context"
	"errors"
	"net"
	"sync"
)

// HostTCPHandler claims intercepted Service destinations on the host TUN path.
// When ok is true, the bridge writes the SOCKS success reply then calls serve.
type HostTCPHandler func(host string, port uint16) (serve func(net.Conn), ok bool)

// HostUDPHandler claims intercepted UDP destinations on the host TUN path.
// dial opens a connection that exchanges raw datagram payloads via Read/Write.
type HostUDPHandler func(host string, port uint16) (dial func(context.Context) (net.Conn, error), ok bool)

type LogHandler func(message string)

type ForwardDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Server struct {
	HostTCP       HostTCPHandler
	HostUDP       HostUDPHandler
	LogHandler    LogHandler
	ForwardDialer ForwardDialer

	dialerMu sync.RWMutex
	hostMu   sync.RWMutex
	logMu    sync.RWMutex
	tasks    *goroutinePool
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

// SetForwardDialer switches ordinary cluster traffic to a standard SOCKS
// upstream backed by the v3 Trojan/WebSocket sing-box process.
func (b *Bridge) SetForwardDialer(dialer ForwardDialer) {
	b.server.dialerMu.Lock()
	b.server.ForwardDialer = dialer
	b.server.dialerMu.Unlock()
}
