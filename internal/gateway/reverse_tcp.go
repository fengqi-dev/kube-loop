package gateway

import (
	"errors"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

func (l *interceptListener) acceptTCP() {
	for {
		conn, err := l.tcp.Accept()
		if err != nil {
			select {
			case <-l.cancel:
				return
			default:
				if errors.Is(err, net.ErrClosed) {
					return
				}
				l.server.logf("tcp accept %s: %v", l.id, err)
				continue
			}
		}
		streamID := l.server.nextStream.Add(1)
		pending := &pendingStream{
			id:      streamID,
			token:   l.control.token,
			network: tunnel.NetworkTCP,
			ready:   make(chan net.Conn, 1),
			tcpConn: conn,
		}
		if !l.server.offerPending(pending) {
			_ = conn.Close()
			continue
		}
		if err := l.control.reply(tunnel.ControlMessage{
			Type:        tunnel.CtrlInboundReady,
			InterceptID: l.id,
			Network:     tunnel.NetworkTCP,
			StreamID:    streamID,
		}); err != nil {
			l.server.takePending(streamID)
			_ = conn.Close()
			return
		}
	}
}
