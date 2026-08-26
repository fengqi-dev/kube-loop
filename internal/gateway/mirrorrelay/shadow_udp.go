package mirrorrelay

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/gateway/trafficlistener"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
)

type udpPrimaryAssociation struct {
	primary  net.Conn
	listener *net.UDPConn
	remote   *net.UDPAddr
	port     entity.Port
	key      string
	lastSeen time.Time
}

type expiredUDPAssociation struct {
	id          uint64
	association *udpPrimaryAssociation
}

func (relay *mirrorRelay) readUDP(ctx context.Context, index int, binding trafficlistener.UDPBinding) error {
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
		key := primaryKey("udp", binding.Port.ServicePort) + "/" + remote.String() + "/" + strconv.Itoa(index)
		id, association, err := relay.udpAssociation(ctx, binding, remote, key)
		if err != nil {
			continue
		}
		payload := append([]byte(nil), buffer[:count]...)
		if err := writeDatagram(association.primary, payload); err != nil {
			relay.removeUDP(id)
			continue
		}
		relay.emit(mirrorstream.Frame{
			Type: mirrorstream.Datagram, StreamID: id,
			// BindListeners validates ServicePort as a positive 16-bit port.
			ServicePort: uint32(binding.Port.ServicePort), //nolint:gosec // Validated as a positive 16-bit port.
			Protocol:    mirrorstream.ProtocolUDP, Payload: payload,
		})
	}
}

func (relay *mirrorRelay) udpAssociation(
	ctx context.Context,
	binding trafficlistener.UDPBinding,
	remote *net.UDPAddr,
	key string,
) (uint64, *udpPrimaryAssociation, error) {
	now := relay.config.Now().UTC()
	relay.mu.Lock()
	if id, exists := relay.udpKeys[key]; exists {
		association := relay.udp[id]
		association.lastSeen = now
		relay.mu.Unlock()
		return id, association, nil
	}
	relay.mu.Unlock()
	dialContext, cancel := context.WithTimeout(ctx, relay.config.PrimaryDialTimeout)
	primary, err := relay.primaries.Dial(dialContext, "udp", binding.Port.ServicePort)
	cancel()
	if err != nil {
		return 0, nil, err
	}
	relay.mu.Lock()
	if id, exists := relay.udpKeys[key]; exists {
		association := relay.udp[id]
		association.lastSeen = now
		relay.mu.Unlock()
		_ = primary.Close()
		return id, association, nil
	}
	id := relay.nextStreamID()
	association := &udpPrimaryAssociation{
		primary: primary, listener: binding.Connection, remote: cloneUDPAddress(remote),
		port: binding.Port, key: key, lastSeen: now,
	}
	relay.udpKeys[key] = id
	relay.udp[id] = association
	relay.mu.Unlock()
	relay.streams.Go(func() {
		relay.readPrimaryUDP(ctx, id, association)
	})
	return id, association, nil
}

func (relay *mirrorRelay) readPrimaryUDP(ctx context.Context, id uint64, association *udpPrimaryAssociation) {
	buffer := make([]byte, 65507)
	for {
		count, err := association.primary.Read(buffer)
		if err != nil {
			if ctx.Err() == nil {
				relay.removeUDP(id)
			}
			return
		}
		if count == 0 {
			continue
		}
		if _, err := association.listener.WriteToUDP(buffer[:count], association.remote); err != nil {
			relay.removeUDP(id)
			return
		}
		relay.mu.Lock()
		if current := relay.udp[id]; current == association {
			association.lastSeen = relay.config.Now().UTC()
		}
		relay.mu.Unlock()
	}
}

func (relay *mirrorRelay) reapUDP(ctx context.Context) error {
	interval := max(relay.config.UDPIdleTimeout/2, 50*time.Millisecond)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cutoff := relay.config.Now().UTC().Add(-relay.config.UDPIdleTimeout)
			for _, expired := range relay.claimExpiredUDP(cutoff) {
				_ = expired.association.primary.Close()
				relay.emit(mirrorstream.Frame{Type: mirrorstream.Close, StreamID: expired.id})
				relay.clearDropped(expired.id)
			}
		}
	}
}

func (relay *mirrorRelay) claimExpiredUDP(
	cutoff time.Time,
) []expiredUDPAssociation {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	expired := make([]expiredUDPAssociation, 0)
	for id, association := range relay.udp {
		if association.lastSeen.After(cutoff) {
			continue
		}
		delete(relay.udp, id)
		delete(relay.udpKeys, association.key)
		expired = append(expired, expiredUDPAssociation{
			id: id, association: association,
		})
	}
	return expired
}

func cloneUDPAddress(address *net.UDPAddr) *net.UDPAddr {
	return &net.UDPAddr{IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: address.Zone}
}
