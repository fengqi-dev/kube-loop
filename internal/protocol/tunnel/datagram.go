package tunnel

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

func WriteDatagram(w io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxDatagramSize {
		return fmt.Errorf("invalid datagram size %d", len(payload))
	}
	var size [2]byte
	// payload is bounded by the uint16-sized MaxDatagramSize above.
	binary.BigEndian.PutUint16(size[:], uint16(len(payload))) //nolint:gosec // Datagram size is bounded.
	if err := writeAll(w, size[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func ReadDatagram(r *bufio.Reader, buffer []byte) ([]byte, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	payloadSize := int(binary.BigEndian.Uint16(size[:]))
	if payloadSize == 0 || payloadSize > MaxDatagramSize {
		return nil, fmt.Errorf("invalid datagram size %d", payloadSize)
	}
	if cap(buffer) < payloadSize {
		buffer = make([]byte, payloadSize)
	}
	buffer = buffer[:payloadSize]
	if _, err := io.ReadFull(r, buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}
