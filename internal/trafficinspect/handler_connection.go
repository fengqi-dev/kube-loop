package trafficinspect

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"sync"
)

type transparentConn struct {
	net.Conn

	done          chan struct{}
	closeOnce     sync.Once
	closeErr      error
	writeAccess   sync.Mutex
	suppressReply bool
}

func newTransparentConn(connection net.Conn) *transparentConn {
	return &transparentConn{
		Conn:          connection,
		done:          make(chan struct{}),
		suppressReply: true,
	}
}

func (c *transparentConn) Write(payload []byte) (int, error) {
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()
	if c.suppressReply && bytes.Equal(payload, []byte(connectEstablished)) {
		c.suppressReply = false
		return len(payload), nil
	}
	c.suppressReply = false
	return c.Conn.Write(payload)
}

func (c *transparentConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.Conn.Close()
		close(c.done)
	})
	return c.closeErr
}

type connectionResponseWriter struct {
	connection *transparentConn
	header     http.Header
}

func (w *connectionResponseWriter) Header() http.Header {
	return w.header
}

func (w *connectionResponseWriter) Write(payload []byte) (int, error) {
	return w.connection.Write(payload)
}

func (w *connectionResponseWriter) WriteHeader(statusCode int) {
	if _, err := fmt.Fprintf(
		w.connection,
		"HTTP/1.1 %d %s\r\n\r\n",
		statusCode,
		http.StatusText(statusCode),
	); err != nil {
		_ = w.connection.Close()
	}
}

func (w *connectionResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	reader := bufio.NewReader(w.connection)
	writer := bufio.NewWriter(w.connection)
	return w.connection, bufio.NewReadWriter(reader, writer), nil
}
