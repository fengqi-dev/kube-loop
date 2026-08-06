package gateway

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

func (l *interceptListener) acceptUDP() {
	buffer := make([]byte, tunnel.MaxDatagramSize)
	associations := make(map[string]*udpAssociation)
	var mu sync.Mutex

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-l.cancel:
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-udpAssociationIdle)
				mu.Lock()
				for key, assoc := range associations {
					assoc.tunnelMu.Lock()
					idle := assoc.tunnel == nil && assoc.lastSeen.Before(cutoff)
					if assoc.tunnel != nil {
						idle = assoc.lastSeen.Before(cutoff)
					}
					if idle {
						if assoc.tunnel != nil {
							_ = assoc.tunnel.Close()
						}
						if assoc.pendingID != 0 {
							if pending := l.server.takePending(assoc.pendingID); pending != nil {
								pending.close()
							}
						}
						delete(associations, key)
					}
					assoc.tunnelMu.Unlock()
				}
				mu.Unlock()
			}
		}
	}()

	for {
		_ = l.udp.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, remote, err := l.udp.ReadFrom(buffer)
		if err != nil {
			select {
			case <-l.cancel:
				mu.Lock()
				for _, assoc := range associations {
					assoc.tunnelMu.Lock()
					if assoc.tunnel != nil {
						_ = assoc.tunnel.Close()
					}
					if assoc.pendingID != 0 {
						if pending := l.server.takePending(assoc.pendingID); pending != nil {
							pending.close()
						}
					}
					assoc.tunnelMu.Unlock()
				}
				mu.Unlock()
				return
			default:
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				if errors.Is(err, net.ErrClosed) {
					return
				}
				l.server.logf("udp read %s: %v", l.id, err)
				continue
			}
		}
		payload := append([]byte(nil), buffer[:n]...)
		key := remote.String()
		mu.Lock()
		assoc := associations[key]
		if assoc != nil {
			assoc.lastSeen = time.Now()
			assoc.tunnelMu.Lock()
			tunnelConn := assoc.tunnel
			assoc.tunnelMu.Unlock()
			if tunnelConn != nil {
				mu.Unlock()
				if err := tunnel.WriteDatagram(tunnelConn, payload); err != nil {
					mu.Lock()
					assoc.tunnelMu.Lock()
					_ = assoc.tunnel.Close()
					assoc.tunnel = nil
					assoc.tunnelMu.Unlock()
					delete(associations, key)
					mu.Unlock()
				}
				continue
			}
			assoc.first = payload
			mu.Unlock()
			continue
		}

		streamID := l.server.nextStream.Add(1)
		assoc = &udpAssociation{
			remote:    remote,
			first:     payload,
			lastSeen:  time.Now(),
			pendingID: streamID,
		}
		associations[key] = assoc
		pending := &pendingStream{
			id:        streamID,
			token:     l.control.token,
			network:   tunnel.NetworkUDP,
			ready:     make(chan net.Conn, 1),
			udpPacket: l.udp,
			assoc:     assoc,
		}
		mu.Unlock()

		if !l.server.offerPending(pending) {
			mu.Lock()
			delete(associations, key)
			mu.Unlock()
			continue
		}
		if err := l.control.reply(tunnel.ControlMessage{
			Type:        tunnel.CtrlInboundReady,
			InterceptID: l.id,
			Network:     tunnel.NetworkUDP,
			StreamID:    streamID,
		}); err != nil {
			l.server.takePending(streamID)
			mu.Lock()
			delete(associations, key)
			mu.Unlock()
			return
		}
	}
}
