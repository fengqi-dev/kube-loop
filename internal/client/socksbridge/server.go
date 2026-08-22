package socksbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/bufferpool"
	"github.com/things-go/go-socks5/statute"

	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
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

// TCPInspector handles one SOCKS CONNECT stream after the success reply has
// been sent. Implementations open their upstream through the supplied bridge
// dialer, so inspection does not require another local proxy listener.
type TCPInspector interface {
	ServeConn(context.Context, net.Conn, string) error
	Close() error
}

// DialContextFunc opens an upstream connection through the current Gateway.
type DialContextFunc func(context.Context, string, string) (net.Conn, error)

// TCPInspectorFactory creates an inspector bound to the bridge dialer.
type TCPInspectorFactory func(DialContextFunc) (TCPInspector, error)

type listenConfig struct {
	inspectorFactory TCPInspectorFactory
}

// ListenOption configures the local SOCKS bridge.
type ListenOption func(*listenConfig) error

// WithTCPInspector enables in-process TCP inspection without adding a listener.
func WithTCPInspector(factory TCPInspectorFactory) ListenOption {
	return func(config *listenConfig) error {
		if factory == nil {
			return errors.New("tCP inspector factory is required")
		}
		config.inspectorFactory = factory
		return nil
	}
}

type Server struct {
	GatewayAddress string
	SessionToken   tunnel.SessionToken
	DialTimeout    time.Duration
	HostTCP        HostTCPHandler
	HostUDP        HostUDPHandler
	LogHandler     LogHandler
	inspector      TCPInspector

	gatewayMu sync.RWMutex
	hostMu    sync.RWMutex
	logMu     sync.RWMutex
}

// Bridge is the local SOCKS listener used by sing-box's kubernetes outbound.
type Bridge struct {
	net.Listener

	server    *Server
	closeOnce sync.Once
	closeErr  error
}

// Close stops the SOCKS listener and releases inspector transports.
func (b *Bridge) Close() error {
	b.closeOnce.Do(func() {
		listenerErr := b.Listener.Close()
		if errors.Is(listenerErr, net.ErrClosed) {
			listenerErr = nil
		}
		b.closeErr = listenerErr
		if b.server.inspector != nil {
			b.closeErr = errors.Join(b.closeErr, b.server.inspector.Close())
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

func (s *Server) Serve(listener net.Listener) error {
	server := socks5.NewServer(
		socks5.WithResolver(remoteResolver{}),
		socks5.WithBufferPool(bufferpool.NewPool(tunnel.MaxDatagramSize+512)),
		socks5.WithDial(s.dial),
		socks5.WithConnectHandle(s.handleConnect),
		socks5.WithAssociateMiddleware(func(
			_ context.Context,
			_ io.Writer,
			request *socks5.Request,
		) error {
			// sing-box may advertise a UDP ASSOCIATE endpoint that differs from
			// the socket it ultimately uses. The bridge only listens on loopback,
			// so accepting the actual source is both safe and interoperable.
			request.DestAddr = &statute.AddrSpec{IP: net.IPv4zero}
			return nil
		}),
	)
	if err := server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (s *Server) handleConnect(
	ctx context.Context,
	writer io.Writer,
	request *socks5.Request,
) (resultErr error) {
	host, port, err := destination(request.DestAddr)
	if err != nil {
		_ = socks5.SendReply(writer, statute.RepAddrTypeNotSupported, nil)
		return err
	}
	s.hostMu.RLock()
	hostTCP := s.HostTCP
	s.hostMu.RUnlock()
	if hostTCP != nil {
		if serve, ok := hostTCP(host, port); ok && serve != nil {
			client, ok := writer.(net.Conn)
			if !ok {
				_ = socks5.SendReply(writer, statute.RepServerFailure, nil)
				return errors.New("sOCKS client is not a network connection")
			}
			if err := socks5.SendReply(writer, statute.RepSuccess, client.LocalAddr()); err != nil {
				return err
			}
			serve(&bufferedConn{Conn: client, reader: request.Reader})
			return nil
		}
	}
	if s.inspector != nil {
		client, ok := writer.(net.Conn)
		if !ok {
			_ = socks5.SendReply(writer, statute.RepServerFailure, nil)
			return errors.New("sOCKS client is not a network connection")
		}
		if err := socks5.SendReply(writer, statute.RepSuccess, client.LocalAddr()); err != nil {
			return err
		}
		target := net.JoinHostPort(host, strconv.Itoa(int(port)))
		if err := s.inspector.ServeConn(ctx, &bufferedConn{Conn: client, reader: request.Reader}, target); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.logf("TCP inspect %s failed: %v", target, err)
			return fmt.Errorf("inspect TCP %s: %w", target, err)
		}
		return nil
	}

	target, err := s.openGateway(ctx, tunnel.CommandTCP, host, port)
	if err != nil {
		_ = socks5.SendReply(writer, statute.RepConnectionRefused, nil)
		return err
	}
	defer func() {
		if err := target.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close SOCKS target: %w", err))
		}
	}()
	if err := socks5.SendReply(writer, statute.RepSuccess, request.LocalAddr); err != nil {
		return err
	}
	relay(writer, request.Reader, target)
	return nil
}

func (s *Server) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split SOCKS destination: %w", err)
	}
	value, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS destination port: %w", err)
	}
	port := uint16(value)
	if network != "udp" {
		return s.openGateway(ctx, tunnel.CommandTCP, host, port)
	}
	s.hostMu.RLock()
	hostUDP := s.HostUDP
	s.hostMu.RUnlock()
	if hostUDP != nil {
		if dial, ok := hostUDP(host, port); ok && dial != nil {
			return dial(ctx)
		}
	}
	connection, err := s.openGateway(ctx, tunnel.CommandUDP, host, port)
	if err != nil {
		return nil, err
	}
	return newFramedConn(connection), nil
}

func (s *Server) openGateway(
	ctx context.Context,
	command byte,
	host string,
	port uint16,
) (net.Conn, error) {
	protocol := "TCP"
	if command == tunnel.CommandUDP {
		protocol = "UDP"
	}
	destination := net.JoinHostPort(host, strconv.Itoa(int(port)))
	s.logf("%s connect %s", protocol, destination)
	timeout := s.DialTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	s.gatewayMu.RLock()
	gatewayAddress := s.GatewayAddress
	sessionToken := s.SessionToken
	s.gatewayMu.RUnlock()
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", gatewayAddress)
	if err != nil {
		s.logf("%s connect %s failed: %v", protocol, destination, err)
		return nil, fmt.Errorf("connect gateway: %w", err)
	}
	request := tunnel.OpenRequest{
		Command: command, Host: host, Port: port,
	}
	if err := tunnel.WriteOpen(connection, request, sessionToken); err != nil {
		closeErr := connection.Close()
		s.logf("%s connect %s failed: %v", protocol, destination, err)
		return nil, errors.Join(err, closeErr)
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		closeErr := connection.Close()
		s.logf("%s connect %s failed: %v", protocol, destination, err)
		return nil, errors.Join(err, closeErr)
	}
	s.logf("%s connected %s", protocol, destination)
	return connection, nil
}

func (s *Server) logf(format string, values ...any) {
	s.logMu.RLock()
	handler := s.LogHandler
	s.logMu.RUnlock()
	if handler != nil {
		handler(fmt.Sprintf(format, values...))
	}
}

func destination(address *statute.AddrSpec) (string, uint16, error) {
	if address == nil || address.Port < 0 || address.Port > 65535 {
		return "", 0, errors.New("invalid SOCKS destination")
	}
	host := address.FQDN
	if host == "" && address.IP != nil {
		host = address.IP.String()
	}
	if host == "" {
		return "", 0, errors.New("empty SOCKS destination")
	}
	return host, uint16(address.Port), nil
}

func relay(client io.Writer, clientReader io.Reader, target net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, clientReader)
		streamcopy.CloseWrite(target)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		streamcopy.CloseWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func Listen(
	ctx context.Context,
	gatewayAddress, listenAddress string,
	token tunnel.SessionToken,
	options ...ListenOption,
) (*Bridge, error) {
	config := listenConfig{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil SOCKS bridge option")
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	server := &Server{GatewayAddress: gatewayAddress, SessionToken: token}
	if config.inspectorFactory != nil {
		server.inspector, err = config.inspectorFactory(server.dial)
		if err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("create SOCKS TCP inspector: %w", err)
		}
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close() // Closing only wakes Serve after cancellation; no caller can act on the result.
	}()
	go func() { _ = server.Serve(listener) }()
	return &Bridge{Listener: listener, server: server}, nil
}
