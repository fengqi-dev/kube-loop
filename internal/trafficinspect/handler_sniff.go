package trafficinspect

import (
	"bufio"
	"bytes"
	"net"
	"time"
)

func sniffInspectableProtocol(connection net.Conn, reader *bufio.Reader) bool {
	deadline := time.Now().Add(protocolSniffTimeout)
	if err := connection.SetReadDeadline(deadline); err == nil {
		defer func() { _ = connection.SetReadDeadline(time.Time{}) }()
	}

	for length := 1; length <= len("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); length++ {
		prefix, err := reader.Peek(length)
		inspectable, possible := classifyInspectablePrefix(prefix)
		if inspectable {
			return true
		}
		if !possible || err != nil {
			return false
		}
	}
	return false
}

func classifyInspectablePrefix(prefix []byte) (inspectable, possible bool) {
	if len(prefix) == 0 {
		return false, true
	}
	for _, preface := range inspectableCleartextPrefaces {
		if bytes.Equal(prefix, preface) {
			return true, true
		}
		if len(prefix) < len(preface) && bytes.Equal(prefix, preface[:len(prefix)]) {
			possible = true
		}
	}
	if tlsClientHelloPrefix(prefix) {
		if len(prefix) == 6 {
			return true, true
		}
		possible = true
	}
	return false, possible
}

func tlsClientHelloPrefix(prefix []byte) bool {
	if len(prefix) > 6 || prefix[0] != 0x16 {
		return false
	}
	if len(prefix) >= 2 && prefix[1] != 0x03 {
		return false
	}
	if len(prefix) >= 3 && (prefix[2] < 0x01 || prefix[2] > 0x04) {
		return false
	}
	if len(prefix) >= 5 && prefix[3] == 0 && prefix[4] == 0 {
		return false
	}
	return len(prefix) < 6 || prefix[5] == 0x01
}
