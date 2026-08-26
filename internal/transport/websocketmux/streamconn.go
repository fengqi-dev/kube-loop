package websocketmux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	streamFrameData byte = 1
	streamFrameFIN  byte = 2
	// MaximumStreamFrameBytes is the payload ceiling of one logical-stream
	// data frame. It is advertised in WSS ServerHello and is protocol-stable.
	MaximumStreamFrameBytes = 64 << 10
)

// StreamConn carries data and half-close markers inside an smux stream. smux
// itself discards unread receive buffers once both native FINs cross, so its
// FIN cannot safely model TCP half-close for request/response protocols.
type StreamConn struct {
	net.Conn

	readMu    sync.Mutex
	readData  []byte
	readEOF   bool
	writeMu   sync.Mutex
	writeEOF  bool
	closeOnce sync.Once
	closeErr  error
	idle      time.Duration
}

func NewStreamConn(connection net.Conn) *StreamConn {
	return NewStreamConnWithIdleTimeout(connection, 0)
}

func NewStreamConnWithIdleTimeout(connection net.Conn, idle time.Duration) *StreamConn {
	stream := &StreamConn{Conn: connection, idle: idle}
	stream.touch()
	return stream
}

func (connection *StreamConn) Read(destination []byte) (int, error) {
	connection.readMu.Lock()
	defer connection.readMu.Unlock()
	if len(destination) == 0 {
		return 0, nil
	}
	if len(connection.readData) > 0 {
		read := copy(destination, connection.readData)
		connection.readData = connection.readData[read:]
		connection.touch()
		return read, nil
	}
	if connection.readEOF {
		return 0, io.EOF
	}

	var header [5]byte
	if _, err := io.ReadFull(connection.Conn, header[:]); err != nil {
		return 0, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	switch header[0] {
	case streamFrameFIN:
		if length != 0 {
			return 0, errors.New("invalid WebSocket stream FIN frame")
		}
		connection.readEOF = true
		return 0, io.EOF
	case streamFrameData:
		if length == 0 || length > MaximumStreamFrameBytes {
			return 0, fmt.Errorf("invalid WebSocket stream data length %d", length)
		}
	default:
		return 0, fmt.Errorf("invalid WebSocket stream frame type %d", header[0])
	}
	connection.readData = make([]byte, int(length))
	if _, err := io.ReadFull(connection.Conn, connection.readData); err != nil {
		connection.readData = nil
		return 0, err
	}
	read := copy(destination, connection.readData)
	connection.readData = connection.readData[read:]
	connection.touch()
	return read, nil
}

func (connection *StreamConn) Write(payload []byte) (int, error) {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if connection.writeEOF {
		return 0, io.ErrClosedPipe
	}
	written := 0
	for len(payload) > 0 {
		size := min(len(payload), MaximumStreamFrameBytes)
		if err := connection.writeFrame(streamFrameData, payload[:size]); err != nil {
			return written, err
		}
		connection.touch()
		written += size
		payload = payload[size:]
	}
	return written, nil
}

func (connection *StreamConn) CloseWrite() error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	if connection.writeEOF {
		return io.ErrClosedPipe
	}
	connection.writeEOF = true
	err := connection.writeFrame(streamFrameFIN, nil)
	if err == nil {
		connection.touch()
	}
	return err
}

func (connection *StreamConn) Close() error {
	connection.closeOnce.Do(func() { connection.closeErr = connection.Conn.Close() })
	return connection.closeErr
}

func (connection *StreamConn) writeFrame(kind byte, payload []byte) error {
	var header [5]byte
	header[0] = kind
	// Stream payloads are bounded by the negotiated uint32 frame limit.
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload))) //nolint:gosec // Frame payload is bounded.
	if err := writeAll(connection.Conn, header[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		return writeAll(connection.Conn, payload)
	}
	return nil
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if written > 0 {
			payload = payload[written:]
		}
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func (connection *StreamConn) touch() {
	if connection.idle > 0 {
		_ = connection.SetDeadline(time.Now().Add(connection.idle))
	}
}
