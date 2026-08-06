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

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/bufferpool"
	"github.com/things-go/go-socks5/statute"
)

// HostTCPHandler claims intercepted Service destinations on the host TUN path.
// When ok is true, the bridge writes the SOCKS success reply then calls serve.
type HostTCPHandler func(host string, port uint16) (serve func(net.Conn), ok bool)

// HostUDPHandler claims intercepted UDP destinations on the host TUN path.
// dial opens a connection that exchanges raw datagram payloads via Read/Write
// (not tunnel length-prefix framing).
type HostUDPHandler func(host string, port uint16) (dial func(context.Context) (net.Conn, error), ok bool)

type Server struct {
	GatewayAddress string
	SessionToken   tunnel.SessionToken
	DialTimeout    time.Duration
	HostTCP        HostTCPHandler
	HostUDP        HostUDPHandler

	gatewayMu sync.RWMutex
}

// Bridge is the local SOCKS listener used by sing-box's kubernetes outbound.
type Bridge struct {
	net.Listener
	server *Server
}

func (b *Bridge) SetHostTCPHandler(handler HostTCPHandler) {
	b.server.HostTCP = handler
}

func (b *Bridge) SetHostUDPHandler(handler HostUDPHandler) {
	b.server.HostUDP = handler
}

// SetGatewayAddress switches new SOCKS requests to a replacement Kubernetes
// API port-forward without interrupting the local sing-box listener.
func (b *Bridge) SetGatewayAddress(address string) {
	b.server.gatewayMu.Lock()
	b.server.GatewayAddress = address
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
) error {
	host, port, err := destination(request.DestAddr)
	if err != nil {
		_ = socks5.SendReply(writer, statute.RepAddrTypeNotSupported, nil)
		return err
	}
	if s.HostTCP != nil {
		if serve, ok := s.HostTCP(host, port); ok && serve != nil {
			client, ok := writer.(net.Conn)
			if !ok {
				_ = socks5.SendReply(writer, statute.RepServerFailure, nil)
				return errors.New("SOCKS client is not a network connection")
			}
			if err := socks5.SendReply(writer, statute.RepSuccess, client.LocalAddr()); err != nil {
				return err
			}
			serve(&bufferedConn{Conn: client, reader: request.Reader})
			return nil
		}
	}

	target, err := s.openGateway(ctx, tunnel.CommandTCP, host, port)
	if err != nil {
		_ = socks5.SendReply(writer, statute.RepConnectionRefused, nil)
		return err
	}
	defer target.Close()
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
	if s.HostUDP != nil {
		if dial, ok := s.HostUDP(host, port); ok && dial != nil {
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
	timeout := s.DialTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	s.gatewayMu.RLock()
	gatewayAddress := s.GatewayAddress
	s.gatewayMu.RUnlock()
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", gatewayAddress)
	if err != nil {
		return nil, fmt.Errorf("connect gateway: %w", err)
	}
	request := tunnel.OpenRequest{
		Command: command, Host: host, Port: port,
	}
	if err := tunnel.WriteOpen(connection, request, s.SessionToken); err != nil {
		connection.Close()
		return nil, err
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
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
		if value, ok := target.(interface{ CloseWrite() error }); ok {
			_ = value.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		if value, ok := client.(interface{ CloseWrite() error }); ok {
			_ = value.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
}

func Listen(
	ctx context.Context,
	gatewayAddress, listenAddress string,
	token tunnel.SessionToken,
) (*Bridge, error) {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	server := &Server{GatewayAddress: gatewayAddress, SessionToken: token}
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	go func() { _ = server.Serve(listener) }()
	return &Bridge{Listener: listener, server: server}, nil
}
