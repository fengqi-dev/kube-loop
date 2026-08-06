package traffic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	socksVersion = 5

	socksMethodNone     = 0
	socksMethodPassword = 2

	socksCommandConnect      = 1
	socksCommandUDPAssociate = 3

	socksAddressIPv4   = 1
	socksAddressDomain = 3
	socksAddressIPv6   = 4
)

// Endpoint describes one loopback sing-box SOCKS inbound.
type Endpoint struct {
	Address  string
	Username string
	Password string
}

// Dialer opens fixed-destination TCP and UDP connections through a SOCKS5
// endpoint. The returned UDP net.Conn hides SOCKS datagram framing from callers.
type Dialer struct {
	Endpoint Endpoint
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return d.dialTCP(ctx, address)
	case "udp", "udp4", "udp6":
		return d.dialUDP(ctx, network, address)
	default:
		return nil, fmt.Errorf("unsupported traffic network %q", network)
	}
}

func (d Dialer) dialTCP(ctx context.Context, address string) (net.Conn, error) {
	conn, reader, err := d.openControl(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeRequest(conn, socksCommandConnect, address); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if _, err := readReply(reader); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SOCKS connect %s: %w", address, err)
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func (d Dialer) dialUDP(ctx context.Context, network, target string) (net.Conn, error) {
	control, reader, err := d.openControl(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeRequest(control, socksCommandUDPAssociate, "0.0.0.0:0"); err != nil {
		_ = control.Close()
		return nil, err
	}
	relayAddress, err := readReply(reader)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("SOCKS UDP associate: %w", err)
	}
	relayHost, relayPort, err := net.SplitHostPort(relayAddress)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("parse SOCKS UDP relay: %w", err)
	}
	if relayHost == "" || relayHost == "0.0.0.0" || relayHost == "::" {
		relayHost, _, err = net.SplitHostPort(d.Endpoint.Address)
		if err != nil {
			_ = control.Close()
			return nil, fmt.Errorf("parse SOCKS endpoint: %w", err)
		}
	}
	relay, err := net.ResolveUDPAddr(network, net.JoinHostPort(relayHost, relayPort))
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("resolve SOCKS UDP relay: %w", err)
	}
	socket, err := net.DialUDP(network, nil, relay)
	if err != nil {
		_ = control.Close()
		return nil, fmt.Errorf("connect SOCKS UDP relay: %w", err)
	}
	targetHost, targetPort, err := splitTarget(target)
	if err != nil {
		_ = socket.Close()
		_ = control.Close()
		return nil, err
	}
	return &udpConn{
		control:    control,
		socket:     socket,
		target:     target,
		targetHost: targetHost,
		targetPort: targetPort,
	}, nil
}

func (d Dialer) openControl(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	if d.Endpoint.Address == "" {
		return nil, nil, errors.New("SOCKS endpoint address is required")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", d.Endpoint.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("connect SOCKS endpoint: %w", err)
	}
	reader := bufio.NewReader(conn)
	methods := []byte{socksMethodNone}
	if d.Endpoint.Username != "" || d.Endpoint.Password != "" {
		methods = []byte{socksMethodPassword}
	}
	if err := writeAll(conn, append([]byte{socksVersion, byte(len(methods))}, methods...)); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(reader, response); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if response[0] != socksVersion {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("unexpected SOCKS version %d", response[0])
	}
	switch response[1] {
	case socksMethodNone:
		if d.Endpoint.Username != "" || d.Endpoint.Password != "" {
			_ = conn.Close()
			return nil, nil, errors.New("SOCKS endpoint skipped required authentication")
		}
	case socksMethodPassword:
		if err := authenticate(conn, reader, d.Endpoint.Username, d.Endpoint.Password); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	default:
		_ = conn.Close()
		return nil, nil, fmt.Errorf("SOCKS authentication method %d rejected", response[1])
	}
	return conn, reader, nil
}

func authenticate(conn net.Conn, reader *bufio.Reader, username, password string) error {
	if len(username) == 0 || len(username) > 255 || len(password) == 0 || len(password) > 255 {
		return errors.New("invalid SOCKS username or password length")
	}
	request := make([]byte, 0, len(username)+len(password)+3)
	request = append(request, 1, byte(len(username)))
	request = append(request, username...)
	request = append(request, byte(len(password)))
	request = append(request, password...)
	if err := writeAll(conn, request); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(reader, response); err != nil {
		return err
	}
	if response[0] != 1 || response[1] != 0 {
		return errors.New("SOCKS authentication failed")
	}
	return nil
}

func writeRequest(writer io.Writer, command byte, address string) error {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse target address %q: %w", address, err)
	}
	parsedPort, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || (parsedPort == 0 && command != socksCommandUDPAssociate) {
		return fmt.Errorf("invalid target port %q", rawPort)
	}
	if host == "" {
		return errors.New("target host is required")
	}
	encoded, err := encodeAddress(host, uint16(parsedPort))
	if err != nil {
		return err
	}
	request := append([]byte{socksVersion, command, 0}, encoded...)
	return writeAll(writer, request)
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func readReply(reader io.Reader) (string, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != socksVersion {
		return "", fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	if header[1] != 0 {
		return "", fmt.Errorf("SOCKS reply status %d", header[1])
	}
	host, port, err := readAddress(reader)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func splitTarget(address string) (string, uint16, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("parse target address %q: %w", address, err)
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("invalid target port %q", rawPort)
	}
	if host == "" {
		return "", 0, errors.New("target host is required")
	}
	return host, uint16(port), nil
}

func encodeAddress(host string, port uint16) ([]byte, error) {
	ip := net.ParseIP(host)
	var encoded []byte
	switch {
	case ip != nil && ip.To4() != nil:
		encoded = append([]byte{socksAddressIPv4}, ip.To4()...)
	case ip != nil:
		encoded = append([]byte{socksAddressIPv6}, ip.To16()...)
	case len(host) > 0 && len(host) <= 255:
		encoded = append([]byte{socksAddressDomain, byte(len(host))}, host...)
	default:
		return nil, fmt.Errorf("invalid SOCKS target host %q", host)
	}
	encoded = binary.BigEndian.AppendUint16(encoded, port)
	return encoded, nil
}

func readAddress(reader io.Reader) (string, uint16, error) {
	var kind [1]byte
	if _, err := io.ReadFull(reader, kind[:]); err != nil {
		return "", 0, err
	}
	var host string
	switch kind[0] {
	case socksAddressIPv4:
		value := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = net.IP(value).String()
	case socksAddressIPv6:
		value := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = net.IP(value).String()
	case socksAddressDomain:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", 0, err
		}
		value := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = string(value)
	default:
		return "", 0, fmt.Errorf("unsupported SOCKS address type %d", kind[0])
	}
	var rawPort [2]byte
	if _, err := io.ReadFull(reader, rawPort[:]); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(rawPort[:]), nil
}

type udpConn struct {
	control    net.Conn
	socket     *net.UDPConn
	target     string
	targetHost string
	targetPort uint16
	closeOnce  sync.Once
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(value []byte) (int, error) { return c.reader.Read(value) }

func (c *udpConn) Read(buffer []byte) (int, error) {
	packet := make([]byte, 65535+512)
	n, err := c.socket.Read(packet)
	if err != nil {
		return 0, err
	}
	payload, err := decodeDatagram(packet[:n])
	if err != nil {
		return 0, err
	}
	if len(payload) > len(buffer) {
		copy(buffer, payload[:len(buffer)])
		return len(buffer), io.ErrShortBuffer
	}
	return copy(buffer, payload), nil
}

func (c *udpConn) Write(payload []byte) (int, error) {
	address, err := encodeAddress(c.targetHost, c.targetPort)
	if err != nil {
		return 0, err
	}
	packet := make([]byte, 0, 3+len(address)+len(payload))
	packet = append(packet, 0, 0, 0)
	packet = append(packet, address...)
	packet = append(packet, payload...)
	if _, err := c.socket.Write(packet); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func decodeDatagram(packet []byte) ([]byte, error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 {
		return nil, errors.New("invalid SOCKS UDP datagram")
	}
	if packet[2] != 0 {
		return nil, errors.New("fragmented SOCKS UDP datagrams are not supported")
	}
	reader := bytes.NewReader(packet[3:])
	if _, _, err := readAddress(reader); err != nil {
		return nil, err
	}
	return packet[len(packet)-reader.Len():], nil
}

func (c *udpConn) Close() error {
	var first error
	c.closeOnce.Do(func() {
		if err := c.socket.Close(); err != nil {
			first = err
		}
		if err := c.control.Close(); err != nil && first == nil {
			first = err
		}
	})
	return first
}

func (c *udpConn) LocalAddr() net.Addr                { return c.socket.LocalAddr() }
func (c *udpConn) RemoteAddr() net.Addr               { return fixedAddr{network: "udp", value: c.target} }
func (c *udpConn) SetDeadline(t time.Time) error      { return c.socket.SetDeadline(t) }
func (c *udpConn) SetReadDeadline(t time.Time) error  { return c.socket.SetReadDeadline(t) }
func (c *udpConn) SetWriteDeadline(t time.Time) error { return c.socket.SetWriteDeadline(t) }

type fixedAddr struct {
	network string
	value   string
}

func (a fixedAddr) Network() string { return a.network }
func (a fixedAddr) String() string  { return a.value }
