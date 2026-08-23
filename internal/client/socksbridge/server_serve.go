package socksbridge

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/bufferpool"
	"github.com/things-go/go-socks5/statute"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func (s *Server) Serve(listener net.Listener) error {
	if s.tasks == nil {
		s.tasks = newGoroutinePool()
	}
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
		socks5.WithGPool(s.tasks),
	)
	if err := server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
