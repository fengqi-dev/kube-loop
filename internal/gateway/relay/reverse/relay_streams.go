package reverse

import (
	"net"
)

func (relay *relaySession) tcpStream(id uint64) *tcpRelayStream {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.tcp[id]
}

func (relay *relaySession) udpAssociationByID(id uint64) *udpRelayAssociation {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	association := relay.udp[id]
	if association != nil {
		association.lastSeen = relay.now().UTC()
	}
	return association
}

func (relay *relaySession) removeTCP(id uint64) bool {
	relay.mu.Lock()
	stream := relay.tcp[id]
	delete(relay.tcp, id)
	relay.mu.Unlock()
	if stream == nil {
		return false
	}
	_ = stream.connection.Close()
	return true
}

func (relay *relaySession) removeStream(id uint64) bool {
	if relay.removeTCP(id) {
		return true
	}
	relay.mu.Lock()
	association := relay.udp[id]
	if association != nil {
		delete(relay.udp, id)
		delete(relay.udpKeys, association.key)
	}
	relay.mu.Unlock()
	return association != nil
}

func (relay *relaySession) closeStreams() {
	relay.mu.Lock()
	streams := make([]net.Conn, 0, len(relay.tcp))
	for _, stream := range relay.tcp {
		streams = append(streams, stream.connection)
	}
	relay.tcp = make(map[uint64]*tcpRelayStream)
	relay.udp = make(map[uint64]*udpRelayAssociation)
	relay.udpKeys = make(map[string]uint64)
	relay.mu.Unlock()
	for _, connection := range streams {
		_ = connection.Close()
	}
}

func (relay *relaySession) nextStreamID() uint64 {
	for {
		id := relay.nextID.Add(1)
		if id != 0 {
			return id
		}
	}
}
