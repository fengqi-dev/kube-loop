package socksbridge

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/things-go/go-socks5/statute"

	"github.com/fengqi-dev/kube-loop/internal/protocol/streamcopy"
)

func (s *Server) logf(format string, values ...any) {
	s.logMu.RLock()
	handler := s.LogHandler
	s.logMu.RUnlock()
	if handler != nil {
		handler(fmt.Sprintf(format, values...))
	}
}

func destination(address *statute.AddrSpec) (string, uint16, error) {
	if address == nil || address.Port < 0 || address.Port > 65535 {
		return "", 0, errors.New("invalid SOCKS destination")
	}
	host := address.FQDN
	if host == "" && address.IP != nil {
		host = address.IP.String()
	}
	if host == "" {
		return "", 0, errors.New("empty SOCKS destination")
	}
	return host, uint16(address.Port), nil
}

func relay(client io.Writer, clientReader io.Reader, target net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, clientReader)
		streamcopy.CloseWrite(target)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		streamcopy.CloseWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}
