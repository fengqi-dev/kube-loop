package socksbridge

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

// remoteResolver preserves domain names so cluster DNS resolution happens at
// the Gateway instead of on the host.
type remoteResolver struct{}

func (remoteResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedConn) Read(value []byte) (int, error) {
	return c.reader.Read(value)
}

// framedConn adapts the Gateway's length-prefixed UDP tunnel to the datagram
// semantics expected by the SOCKS server.
type framedConn struct {
	net.Conn
	reader  *bufio.Reader
	buffer  []byte
	writeMu sync.Mutex
}

func newFramedConn(connection net.Conn) *framedConn {
	return &framedConn{Conn: connection, reader: bufio.NewReader(connection)}
}

func (c *framedConn) Read(destination []byte) (int, error) {
	payload, err := tunnel.ReadDatagram(c.reader, c.buffer)
	if err != nil {
		return 0, err
	}
	c.buffer = payload[:0]
	read := copy(destination, payload)
	if read != len(payload) {
		return read, io.ErrShortBuffer
	}
	return read, nil
}

func (c *framedConn) Write(payload []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := tunnel.WriteDatagram(c.Conn, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}
