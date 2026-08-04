package socksbridge

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

const (
	socksVersion  = 5
	methodNone    = 0
	commandTCP    = 1
	commandUDP    = 3
	addressIPv4   = 1
	addressDomain = 3
	addressIPv6   = 4
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
	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(connection)
	}
}

func (s *Server) handle(control net.Conn) {
	defer control.Close()
	reader := bufio.NewReader(control)
	if err := negotiate(reader, control); err != nil {
		return
	}
	command, host, port, err := readRequest(reader)
	if err != nil {
		_ = writeReply(control, 1, nil)
		return
	}
	switch command {
	case commandTCP:
		s.handleTCP(control, host, port)
	case commandUDP:
		s.handleUDP(control)
	default:
		_ = writeReply(control, 7, nil)
	}
}

func (s *Server) handleTCP(client net.Conn, host string, port uint16) {
	if s.HostTCP != nil {
		if serve, ok := s.HostTCP(host, port); ok && serve != nil {
			if err := writeReply(client, 0, client.LocalAddr()); err != nil {
				return
			}
			serve(client)
			return
		}
	}
	gateway, err := s.openGateway(tunnel.CommandTCP, host, port)
	if err != nil {
		_ = writeReply(client, 5, nil)
		return
	}
	defer gateway.Close()
	if err := writeReply(client, 0, client.LocalAddr()); err != nil {
		return
	}
	relay(client, gateway)
}

func (s *Server) handleUDP(control net.Conn) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		_ = writeReply(control, 1, nil)
		return
	}
	defer listener.Close()
	if err := writeReply(control, 0, listener.LocalAddr()); err != nil {
		return
	}
	association := &udpAssociation{
		server: s, listener: listener, tunnels: make(map[string]*udpTunnel),
	}
	go association.serve()
	_, _ = io.Copy(io.Discard, control)
	association.close()
}

func (s *Server) openGateway(command byte, host string, port uint16) (net.Conn, error) {
	timeout := s.DialTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	s.gatewayMu.RLock()
	gatewayAddress := s.GatewayAddress
	s.gatewayMu.RUnlock()
	connection, err := net.DialTimeout("tcp", gatewayAddress, timeout)
	if err != nil {
		return nil, fmt.Errorf("connect gateway: %w", err)
	}
	if err := tunnel.WriteOpen(connection, tunnel.OpenRequest{
		Command: command, Host: host, Port: port,
	}); err != nil {
		connection.Close()
		return nil, err
	}
	if err := tunnel.ReadStatus(connection); err != nil {
		connection.Close()
		return nil, err
	}
	return connection, nil
}

type udpAssociation struct {
	server   *Server
	listener *net.UDPConn
	mu       sync.Mutex
	client   *net.UDPAddr
	tunnels  map[string]*udpTunnel
	closed   bool
}

type udpTunnel struct {
	connection net.Conn
	host       string
	port       uint16
	// framed is true for Gateway UDP tunnels (length-prefixed datagrams).
	// HostUDP bypass connections use raw Read/Write payloads.
	framed  bool
	writeMu sync.Mutex
}

func (a *udpAssociation) serve() {
	buffer := make([]byte, tunnel.MaxDatagramSize+512)
	for {
		read, client, err := a.listener.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		host, port, payload, err := parseUDPPacket(buffer[:read])
		if err != nil {
			continue
		}
		a.mu.Lock()
		a.client = client
		item := a.tunnels[net.JoinHostPort(host, strconv.Itoa(int(port)))]
		a.mu.Unlock()
		if item == nil {
			item, err = a.newTunnel(host, port)
			if err != nil {
				continue
			}
		}
		item.writeMu.Lock()
		if item.framed {
			err = tunnel.WriteDatagram(item.connection, payload)
		} else {
			_, err = item.connection.Write(payload)
		}
		item.writeMu.Unlock()
		if err != nil {
			a.removeTunnel(item)
		}
	}
}

func (a *udpAssociation) newTunnel(host string, port uint16) (*udpTunnel, error) {
	connection, framed, err := a.openUDP(host, port)
	if err != nil {
		return nil, err
	}
	item := &udpTunnel{connection: connection, host: host, port: port, framed: framed}
	key := net.JoinHostPort(host, strconv.Itoa(int(port)))
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		connection.Close()
		return nil, net.ErrClosed
	}
	if existing := a.tunnels[key]; existing != nil {
		a.mu.Unlock()
		connection.Close()
		return existing, nil
	}
	a.tunnels[key] = item
	a.mu.Unlock()
	go a.readReplies(item)
	return item, nil
}

func (a *udpAssociation) openUDP(host string, port uint16) (net.Conn, bool, error) {
	if a.server.HostUDP != nil {
		if dial, ok := a.server.HostUDP(host, port); ok && dial != nil {
			conn, err := dial(context.Background())
			if err != nil {
				return nil, false, err
			}
			return conn, false, nil
		}
	}
	conn, err := a.server.openGateway(tunnel.CommandUDP, host, port)
	if err != nil {
		return nil, false, err
	}
	return conn, true, nil
}

func (a *udpAssociation) readReplies(item *udpTunnel) {
	if item.framed {
		a.readFramedReplies(item)
		return
	}
	buffer := make([]byte, tunnel.MaxDatagramSize)
	for {
		n, err := item.connection.Read(buffer)
		if err != nil {
			a.removeTunnel(item)
			return
		}
		packet, err := encodeUDPPacket(item.host, item.port, buffer[:n])
		if err != nil {
			continue
		}
		a.mu.Lock()
		client := a.client
		a.mu.Unlock()
		if client != nil {
			_, _ = a.listener.WriteToUDP(packet, client)
		}
	}
}

func (a *udpAssociation) readFramedReplies(item *udpTunnel) {
	reader := bufio.NewReader(item.connection)
	var buffer []byte
	for {
		payload, err := tunnel.ReadDatagram(reader, buffer)
		if err != nil {
			a.removeTunnel(item)
			return
		}
		buffer = payload[:0]
		packet, err := encodeUDPPacket(item.host, item.port, payload)
		if err != nil {
			continue
		}
		a.mu.Lock()
		client := a.client
		a.mu.Unlock()
		if client != nil {
			_, _ = a.listener.WriteToUDP(packet, client)
		}
	}
}

func (a *udpAssociation) removeTunnel(item *udpTunnel) {
	key := net.JoinHostPort(item.host, strconv.Itoa(int(item.port)))
	a.mu.Lock()
	if a.tunnels[key] == item {
		delete(a.tunnels, key)
	}
	a.mu.Unlock()
	item.connection.Close()
}

func (a *udpAssociation) close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	items := make([]*udpTunnel, 0, len(a.tunnels))
	for _, item := range a.tunnels {
		items = append(items, item)
	}
	a.tunnels = make(map[string]*udpTunnel)
	a.mu.Unlock()
	a.listener.Close()
	for _, item := range items {
		item.connection.Close()
	}
}

func negotiate(reader *bufio.Reader, writer io.Writer) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != socksVersion || header[1] == 0 {
		return errors.New("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	if slices.Contains(methods, methodNone) {
		_, err := writer.Write([]byte{socksVersion, methodNone})
		return err
	}
	_, _ = writer.Write([]byte{socksVersion, 0xff})
	return errors.New("SOCKS client does not support no-auth")
}

func readRequest(reader *bufio.Reader) (byte, string, uint16, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, "", 0, err
	}
	if header[0] != socksVersion || header[2] != 0 {
		return 0, "", 0, errors.New("invalid SOCKS request")
	}
	host, err := readAddress(reader, header[3])
	if err != nil {
		return 0, "", 0, err
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return 0, "", 0, err
	}
	return header[1], host, binary.BigEndian.Uint16(port[:]), nil
}

func parseUDPPacket(packet []byte) (string, uint16, []byte, error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return "", 0, nil, errors.New("fragmented or invalid SOCKS UDP packet")
	}
	reader := bufio.NewReaderSize(newSliceReader(packet[4:]), len(packet))
	host, err := readAddress(reader, packet[3])
	if err != nil {
		return "", 0, nil, err
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return "", 0, nil, err
	}
	payload, err := io.ReadAll(reader)
	return host, binary.BigEndian.Uint16(port[:]), payload, err
}

func encodeUDPPacket(host string, port uint16, payload []byte) ([]byte, error) {
	address, err := encodeAddress(host)
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 3+len(address)+2+len(payload))
	copy(packet[3:], address)
	offset := 3 + len(address)
	binary.BigEndian.PutUint16(packet[offset:offset+2], port)
	copy(packet[offset+2:], payload)
	return packet, nil
}

func readAddress(reader io.Reader, addressType byte) (string, error) {
	switch addressType {
	case addressIPv4:
		value := make([]byte, net.IPv4len)
		_, err := io.ReadFull(reader, value)
		return net.IP(value).String(), err
	case addressIPv6:
		value := make([]byte, net.IPv6len)
		_, err := io.ReadFull(reader, value)
		return net.IP(value).String(), err
	case addressDomain:
		var size [1]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return "", err
		}
		if size[0] == 0 {
			return "", errors.New("empty SOCKS domain")
		}
		value := make([]byte, int(size[0]))
		_, err := io.ReadFull(reader, value)
		return string(value), err
	default:
		return "", fmt.Errorf("unsupported SOCKS address type %d", addressType)
	}
}

func encodeAddress(host string) ([]byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			return append([]byte{addressIPv4}, ipv4...), nil
		}
		return append([]byte{addressIPv6}, ip.To16()...), nil
	}
	if len(host) == 0 || len(host) > 255 {
		return nil, errors.New("SOCKS domain length is invalid")
	}
	return append([]byte{addressDomain, byte(len(host))}, []byte(host)...), nil
}

func writeReply(writer io.Writer, status byte, address net.Addr) error {
	host := "0.0.0.0"
	port := uint16(0)
	if value, ok := address.(*net.TCPAddr); ok {
		host, port = value.IP.String(), uint16(value.Port)
	}
	if value, ok := address.(*net.UDPAddr); ok {
		host, port = value.IP.String(), uint16(value.Port)
	}
	encoded, err := encodeAddress(host)
	if err != nil {
		return err
	}
	reply := append([]byte{socksVersion, status, 0}, encoded...)
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], port)
	reply = append(reply, portBytes[:]...)
	_, err = writer.Write(reply)
	return err
}

func relay(left, right net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(left, right); done <- struct{}{} }()
	go func() { _, _ = io.Copy(right, left); done <- struct{}{} }()
	<-done
}

type sliceReader struct {
	value []byte
}

func newSliceReader(value []byte) *sliceReader { return &sliceReader{value: value} }

func (r *sliceReader) Read(destination []byte) (int, error) {
	if len(r.value) == 0 {
		return 0, io.EOF
	}
	read := copy(destination, r.value)
	r.value = r.value[read:]
	return read, nil
}

func Listen(ctx context.Context, gatewayAddress, listenAddress string) (*Bridge, error) {
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, err
	}
	server := &Server{GatewayAddress: gatewayAddress}
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	go func() { _ = server.Serve(listener) }()
	return &Bridge{Listener: listener, server: server}, nil
}
