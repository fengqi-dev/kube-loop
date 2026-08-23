package traffic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

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
		return nil, fmt.Errorf("sOCKS UDP associate: %w", err)
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

type udpConn struct {
	control    net.Conn
	socket     *net.UDPConn
	target     string
	targetHost string
	targetPort uint16
	closeOnce  sync.Once
	closeErr   error
}

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
	c.closeOnce.Do(func() {
		if err := c.socket.Close(); err != nil {
			c.closeErr = err
		}
		if err := c.control.Close(); err != nil && c.closeErr == nil {
			c.closeErr = err
		}
	})
	return c.closeErr
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
