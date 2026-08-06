package gateway

import (
	"bufio"
	"net"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

func (s *Server) offerPending(pending *pendingStream) bool {
	pending.timer = time.AfterFunc(pendingAcceptTimeout, func() {
		if taken := s.takePending(pending.id); taken != nil {
			taken.close()
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.pending[pending.id]; exists {
		return false
	}
	s.pending[pending.id] = pending
	return true
}

func (s *Server) takePending(streamID uint64) *pendingStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pending[streamID]
	delete(s.pending, streamID)
	if pending != nil && pending.timer != nil {
		pending.timer.Stop()
	}
	return pending
}

func (p *pendingStream) close() {
	if p.tcpConn != nil {
		_ = p.tcpConn.Close()
	}
}

func (p *pendingStream) serve(tunnelConn net.Conn) {
	switch p.network {
	case tunnel.NetworkTCP:
		defer tunnelConn.Close()
		defer p.tcpConn.Close()
		relayTCP(tunnelConn, p.tcpConn)
	case tunnel.NetworkUDP:
		p.serveUDP(tunnelConn)
	default:
		_ = tunnelConn.Close()
	}
}

func (p *pendingStream) serveUDP(tunnelConn net.Conn) {
	assoc := p.assoc
	assoc.tunnelMu.Lock()
	assoc.tunnel = tunnelConn
	assoc.pendingID = 0
	first := assoc.first
	assoc.first = nil
	remote := assoc.remote
	assoc.tunnelMu.Unlock()

	if len(first) > 0 {
		if err := tunnel.WriteDatagram(tunnelConn, first); err != nil {
			_ = tunnelConn.Close()
			return
		}
	}

	// Own desktop → cluster UDP writes; inbound packets are demuxed by acceptUDP.
	reader := bufio.NewReader(tunnelConn)
	var buffer []byte
	packetConn := p.packetConn()
	for {
		_ = tunnelConn.SetReadDeadline(time.Now().Add(udpAssociationIdle))
		payload, err := tunnel.ReadDatagram(reader, buffer)
		if err != nil {
			_ = tunnelConn.Close()
			assoc.tunnelMu.Lock()
			if assoc.tunnel == tunnelConn {
				assoc.tunnel = nil
			}
			assoc.tunnelMu.Unlock()
			return
		}
		buffer = payload[:0]
		assoc.lastSeen = time.Now()
		if packetConn == nil {
			_ = tunnelConn.Close()
			return
		}
		if _, err := packetConn.WriteTo(payload, remote); err != nil {
			_ = tunnelConn.Close()
			assoc.tunnelMu.Lock()
			if assoc.tunnel == tunnelConn {
				assoc.tunnel = nil
			}
			assoc.tunnelMu.Unlock()
			return
		}
	}
}

func (p *pendingStream) packetConn() net.PacketConn {
	return p.udpPacket
}

func networkName(network byte) string {
	if network == tunnel.NetworkUDP {
		return "udp"
	}
	return "tcp"
}
