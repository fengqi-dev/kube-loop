package socksbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

func (s *Server) handleConnect(
	ctx context.Context,
	writer io.Writer,
	request *socks5.Request,
) (resultErr error) {
	host, port, err := destination(request.DestAddr)
	if err != nil {
		_ = socks5.SendReply(writer, statute.RepAddrTypeNotSupported, nil)
		return err
	}
	s.hostMu.RLock()
	hostTCP := s.HostTCP
	s.hostMu.RUnlock()
	if hostTCP != nil {
		if serve, ok := hostTCP(host, port); ok && serve != nil {
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
	defer func() {
		if err := target.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close SOCKS target: %w", err))
		}
	}()
	if err := socks5.SendReply(writer, statute.RepSuccess, request.LocalAddr); err != nil {
		return err
	}
	relay(writer, request.Reader, target)
	return nil
}
