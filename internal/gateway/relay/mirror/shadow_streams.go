package mirror

import (
	"io"
	"net"
)

func (relay *mirrorRelay) removeTCP(id uint64) {
	relay.mu.Lock()
	stream := relay.tcp[id]
	delete(relay.tcp, id)
	relay.mu.Unlock()
	if stream == nil {
		return
	}
	_ = stream.client.Close()
	_ = stream.primary.Close()
}

func (relay *mirrorRelay) removeUDP(id uint64) {
	relay.mu.Lock()
	association := relay.udp[id]
	if association != nil {
		delete(relay.udp, id)
		delete(relay.udpKeys, association.key)
	}
	relay.mu.Unlock()
	if association == nil {
		return
	}
	_ = association.primary.Close()
}

func (relay *mirrorRelay) closeStreams() {
	relay.mu.Lock()
	tcpStreams := make([]*tcpPrimaryStream, 0, len(relay.tcp))
	for _, stream := range relay.tcp {
		tcpStreams = append(tcpStreams, stream)
	}
	udpStreams := make([]*udpPrimaryAssociation, 0, len(relay.udp))
	for _, stream := range relay.udp {
		udpStreams = append(udpStreams, stream)
	}
	relay.tcp = make(map[uint64]*tcpPrimaryStream)
	relay.udp = make(map[uint64]*udpPrimaryAssociation)
	relay.udpKeys = make(map[string]uint64)
	relay.mu.Unlock()
	for _, stream := range tcpStreams {
		_ = stream.client.Close()
		_ = stream.primary.Close()
	}
	for _, stream := range udpStreams {
		_ = stream.primary.Close()
	}
}

func (relay *mirrorRelay) nextStreamID() uint64 {
	for {
		id := relay.nextID.Add(1)
		if id != 0 {
			return id
		}
	}
}

func writePrimary(connection net.Conn, payload []byte) error {
	for len(payload) > 0 {
		count, err := connection.Write(payload)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		payload = payload[count:]
	}
	return nil
}

func writeDatagram(connection net.Conn, payload []byte) error {
	count, err := connection.Write(payload)
	if err != nil {
		return err
	}
	if count != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

func closeWrite(connection net.Conn) {
	if halfCloser, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
	}
}
