package reverserelay

import (
	"errors"
	"io"
	"net"
	"strconv"
)

type localConnection struct {
	connection net.Conn
	target     Target
}

func reverseServicePort(value uint32) (int32, error) {
	if value == 0 || value > 65535 {
		return 0, errors.New("gateway supplied an invalid reverse service port")
	}
	return int32(value), nil
}

func encodedReverseServicePort(value int32) (uint32, error) {
	if value < 1 || value > 65535 {
		return 0, errors.New("reverse target has an invalid service port")
	}
	return uint32(value), nil
}

func (relay *Relay) connection(items map[uint64]*localConnection, id uint64) *localConnection {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return items[id]
}

func (relay *Relay) hasStream(id uint64) bool {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.tcp[id] != nil || relay.udp[id] != nil
}

func (relay *Relay) remove(items map[uint64]*localConnection, id uint64) bool {
	relay.mu.Lock()
	stream := items[id]
	delete(items, id)
	relay.mu.Unlock()
	if stream == nil {
		return false
	}
	_ = stream.connection.Close()
	return true
}

func (relay *Relay) removeAny(id uint64) bool {
	if relay.remove(relay.tcp, id) {
		return true
	}
	return relay.remove(relay.udp, id)
}

func (relay *Relay) closeAll() {
	relay.mu.Lock()
	connections := make([]net.Conn, 0, len(relay.tcp)+len(relay.udp))
	for _, stream := range relay.tcp {
		connections = append(connections, stream.connection)
	}
	for _, stream := range relay.udp {
		connections = append(connections, stream.connection)
	}
	clear(relay.tcp)
	clear(relay.udp)
	relay.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func localAddress(target Target) string {
	return net.JoinHostPort(target.LocalHost, strconv.Itoa(int(target.LocalPort)))
}

func writeLocal(connection net.Conn, payload []byte) error {
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
