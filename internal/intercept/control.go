package intercept

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

// errControlClosed is returned when the Gateway control channel has dropped
// (port-forward died, Gateway pod restarted, etc.).
var errControlClosed = errors.New("gateway control channel closed; disconnect and reconnect")

type controlClient struct {
	address string
	conn    net.Conn

	writeMu sync.Mutex
	replyCh chan tunnel.ControlMessage
	closed  atomic.Bool

	onReady func(interceptID string, network byte, streamID uint64)
	onClose func()
}

func dialControl(
	ctx context.Context,
	gatewayAddress string,
	token tunnel.SessionToken,
	onReady func(interceptID string, network byte, streamID uint64),
	onClose func(),
) (*controlClient, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", gatewayAddress)
	if err != nil {
		return nil, fmt.Errorf("dial gateway control: %w", err)
	}
	if err := tunnel.WriteControlSession(conn, token); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := tunnel.ReadStatus(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("control handshake: %w", err)
	}
	client := &controlClient{
		address: gatewayAddress,
		conn:    conn,
		replyCh: make(chan tunnel.ControlMessage, 8),
		onReady: onReady,
		onClose: onClose,
	}
	go client.readLoop()
	return client, nil
}

func (c *controlClient) readLoop() {
	defer func() {
		c.closed.Store(true)
		close(c.replyCh)
		if c.onClose != nil {
			c.onClose()
		}
	}()
	for {
		message, err := tunnel.ReadControlMessage(c.conn)
		if err != nil {
			return
		}
		switch message.Type {
		case tunnel.CtrlInboundReady:
			if c.onReady != nil {
				c.onReady(message.InterceptID, message.Network, message.StreamID)
			}
		case tunnel.CtrlAck, tunnel.CtrlError:
			select {
			case c.replyCh <- message:
			default:
			}
		}
	}
}

func (c *controlClient) register(interceptID string, network byte, listenPort uint16) error {
	return c.roundTrip(tunnel.ControlMessage{
		Type:        tunnel.CtrlRegister,
		InterceptID: interceptID,
		Network:     network,
		ListenPort:  listenPort,
	})
}

func (c *controlClient) unregister(interceptID string) error {
	return c.roundTrip(tunnel.ControlMessage{
		Type:        tunnel.CtrlUnregister,
		InterceptID: interceptID,
	})
}

func (c *controlClient) roundTrip(message tunnel.ControlMessage) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed.Load() {
		return errControlClosed
	}
	if err := tunnel.WriteControlMessage(c.conn, message); err != nil {
		return err
	}
	select {
	case reply, ok := <-c.replyCh:
		if !ok {
			return errControlClosed
		}
		if reply.Type == tunnel.CtrlError {
			return fmt.Errorf("%s", reply.Error)
		}
		if reply.Type != tunnel.CtrlAck {
			return fmt.Errorf("unexpected control reply type %d", reply.Type)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timed out waiting for control ack")
	}
}

func (c *controlClient) close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func acceptStream(
	ctx context.Context,
	gatewayAddress string,
	token tunnel.SessionToken,
	streamID uint64,
) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", gatewayAddress)
	if err != nil {
		return nil, err
	}
	if err := tunnel.WriteAccept(conn, streamID, token); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := tunnel.ReadStatus(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func relayTCP(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyStream := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if value, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = value.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyStream(left, right)
	go copyStream(right, left)
	<-done
}

func relayUDPConn(tunnelConn, local net.Conn) {
	defer tunnelConn.Close()
	defer local.Close()

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	go func() {
		defer stop()
		reader := bufio.NewReader(tunnelConn)
		var buffer []byte
		for {
			payload, err := tunnel.ReadDatagram(reader, buffer)
			if err != nil {
				return
			}
			buffer = payload[:0]
			if _, err := local.Write(payload); err != nil {
				return
			}
		}
	}()

	buffer := make([]byte, tunnel.MaxDatagramSize)
	for {
		select {
		case <-done:
			return
		default:
		}
		_ = local.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := local.Read(buffer)
		if err != nil {
			stop()
			<-done
			return
		}
		if err := tunnel.WriteDatagram(tunnelConn, buffer[:n]); err != nil {
			stop()
			<-done
			return
		}
	}
}
