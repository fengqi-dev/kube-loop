package trafficinspect

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/transport/streamcopy"
)

func (h *Handler) relayUninspected(ctx context.Context, client net.Conn, target string) error {
	upstream, err := h.dialContext(ctx, "tcp", target)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("dial uninspected traffic target %s: %w", target, err)
	}
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
	}()

	done := make(chan struct{})
	go func() {
		streamcopy.Bidirectional(client, upstream)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = client.Close()
		_ = upstream.Close()
		<-done
		return context.Cause(ctx)
	}
}

type bufferedConn struct {
	net.Conn

	reader *bufio.Reader
}

func (c *bufferedConn) Read(payload []byte) (int, error) {
	return c.reader.Read(payload)
}

func (c *bufferedConn) CloseWrite() error {
	if writer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return c.Close()
}

func canonicalAuthority(authority, scheme string) string {
	host, port, err := net.SplitHostPort(authority)
	if err == nil {
		return strings.ToLower(net.JoinHostPort(strings.TrimSuffix(host, "."), port))
	}
	host = strings.TrimSuffix(strings.Trim(authority, "[]"), ".")
	if strings.EqualFold(scheme, "http") {
		port = "80"
	} else {
		port = "443"
	}
	return strings.ToLower(net.JoinHostPort(host, port))
}
