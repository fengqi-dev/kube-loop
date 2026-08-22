package traffic

import (
	"bufio"
	"context"
	"fmt"
	"net"
)

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
		return nil, fmt.Errorf("sOCKS connect %s: %w", address, err)
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn

	reader *bufio.Reader
}

func (c *bufferedConn) Read(value []byte) (int, error) { return c.reader.Read(value) }

func (c *bufferedConn) CloseWrite() error {
	if writer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return c.Close()
}
