package socksbridge

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/things-go/go-socks5/statute"

	"github.com/fengqi-dev/kube-loop/internal/transport/streamcopy"
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

// relay copies between the SOCKS client and the target until both directions
// end. The client halves stay separate because the handshake reader is
// buffered ahead of the raw connection.
func relay(client io.Writer, clientReader io.Reader, target net.Conn) {
	streamcopy.Relay(
		streamcopy.Side{Reader: clientReader, Writer: client},
		streamcopy.ConnSide(target),
		streamcopy.Options{},
	)
}
