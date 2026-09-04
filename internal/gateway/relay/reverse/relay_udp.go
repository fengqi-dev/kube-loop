package reverse

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/relay/listener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type udpRelayAssociation struct {
	connection *net.UDPConn
	remote     *net.UDPAddr
	port       servicemodel.Port
	key        string
	lastSeen   time.Time
}

func (relay *relaySession) readUDP(ctx context.Context, index int, binding listener.UDPBinding) error {
	buffer := make([]byte, 65507)
	for {
		count, remote, err := binding.Connection.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // A closed UDP socket is the normal relay shutdown signal.
			}
			return err
		}
		if count == 0 {
			continue
		}
		key := fmt.Sprintf("%d/%s", index, remote.String())
		id := relay.udpAssociation(binding, remote, key)
		payload := append([]byte(nil), buffer[:count]...)
		if err := relay.write(ctx, exchangestream.Frame{
			Type: exchangestream.Datagram, StreamID: id,
			// BindListeners validates ServicePort as a positive 16-bit port.
			ServicePort: uint32(binding.Port.ServicePort), //nolint:gosec // Validated as a positive 16-bit port.
			Protocol:    exchangestream.ProtocolUDP, Payload: payload,
		}); err != nil {
			return err
		}
	}
}

func (relay *relaySession) udpAssociation(binding listener.UDPBinding, remote *net.UDPAddr, key string) uint64 {
	now := relay.now().UTC()
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if id, exists := relay.udpKeys[key]; exists {
		relay.udp[id].lastSeen = now
		return id
	}
	id := relay.nextStreamID()
	relay.udpKeys[key] = id
	relay.udp[id] = &udpRelayAssociation{
		connection: binding.Connection,
		remote:     utils.CloneUDPAddress(remote),
		port:       binding.Port,
		key:        key,
		lastSeen:   now,
	}
	return id
}

func (relay *relaySession) reapUDP(ctx context.Context) error {
	interval := max(relay.idle/2, 50*time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cutoff := relay.now().UTC().Add(-relay.idle)
			var expired []uint64
			relay.mu.Lock()
			for id, association := range relay.udp {
				if !association.lastSeen.After(cutoff) {
					delete(relay.udp, id)
					delete(relay.udpKeys, association.key)
					expired = append(expired, id)
				}
			}
			relay.mu.Unlock()
			for _, id := range expired {
				if err := relay.write(ctx, exchangestream.Frame{Type: exchangestream.Close, StreamID: id}); err != nil {
					return err
				}
			}
		}
	}
}
