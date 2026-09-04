// Package streamframe holds the frame layout shared by the stream-multiplexing
// contracts. It is an implementation detail of those contracts, not a contract
// itself: exchangestream and mirrorstream each keep their own Frame type,
// constants, and error wording, so either can diverge from this layout without
// disturbing the other.
package streamframe

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Ready      byte = 1
	Open       byte = 2
	Data       byte = 3
	CloseWrite byte = 4
	Close      byte = 5
	Datagram   byte = 6
	Stop       byte = 7

	ProtocolTCP byte = 1
	ProtocolUDP byte = 2

	MaximumData     = 256 << 10
	MaximumError    = 4096
	MaximumDatagram = 65507

	headerSize = 14
)

// Frame is one multiplexed frame: a fixed 14-byte header followed by payload.
type Frame struct {
	Type        byte
	StreamID    uint64
	ServicePort uint32
	Protocol    byte
	Payload     []byte
}

// Codec encodes and decodes frames for one named contract. Name appears in
// every error message, so a caller can tell which contract rejected a frame.
type Codec struct {
	Name string
}

func (codec Codec) Encode(frame Frame) ([]byte, error) {
	if err := codec.Validate(frame); err != nil {
		return nil, err
	}
	encoded := make([]byte, headerSize+len(frame.Payload))
	encoded[0] = frame.Type
	binary.BigEndian.PutUint64(encoded[1:9], frame.StreamID)
	binary.BigEndian.PutUint32(encoded[9:13], frame.ServicePort)
	encoded[13] = frame.Protocol
	copy(encoded[headerSize:], frame.Payload)
	return encoded, nil
}

func (codec Codec) Decode(encoded []byte) (Frame, error) {
	if len(encoded) < headerSize {
		return Frame{}, errors.New(codec.Name + " frame is truncated")
	}
	frame := Frame{
		Type:        encoded[0],
		StreamID:    binary.BigEndian.Uint64(encoded[1:9]),
		ServicePort: binary.BigEndian.Uint32(encoded[9:13]),
		Protocol:    encoded[13],
		Payload:     append([]byte(nil), encoded[headerSize:]...),
	}
	if err := codec.Validate(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

// Validate reports whether frame is well formed for its type.
func (codec Codec) Validate(frame Frame) error {
	var valid bool
	var name string
	switch frame.Type {
	case Ready:
		valid, name = validControlFrame(frame, false), "ready"
	case Open:
		valid, name = frame.StreamID != 0 && frame.ServicePort != 0 &&
			frame.Protocol == ProtocolTCP && len(frame.Payload) == 0, "open"
	case Data:
		valid, name = validPayloadFrame(frame, MaximumData), "data"
	case CloseWrite:
		valid, name = validControlFrame(frame, true), "half-close"
	case Close:
		valid, name = validTerminalFrame(frame, true), "close"
	case Datagram:
		valid, name = frame.StreamID != 0 && frame.ServicePort != 0 &&
			frame.Protocol == ProtocolUDP && len(frame.Payload) > 0 &&
			len(frame.Payload) <= MaximumDatagram, "datagram"
	case Stop:
		valid, name = validTerminalFrame(frame, false), "stop"
	default:
		return fmt.Errorf("unsupported %s frame type %d", codec.Name, frame.Type)
	}
	if !valid {
		return fmt.Errorf("%s %s frame is invalid", codec.Name, name)
	}
	return nil
}

func validControlFrame(frame Frame, streamRequired bool) bool {
	return (frame.StreamID != 0) == streamRequired && frame.ServicePort == 0 &&
		frame.Protocol == 0 && len(frame.Payload) == 0
}

func validPayloadFrame(frame Frame, maximum int) bool {
	return frame.StreamID != 0 && frame.ServicePort == 0 && frame.Protocol == 0 &&
		len(frame.Payload) > 0 && len(frame.Payload) <= maximum
}

func validTerminalFrame(frame Frame, streamRequired bool) bool {
	return (frame.StreamID != 0) == streamRequired && frame.ServicePort == 0 &&
		frame.Protocol == 0 && len(frame.Payload) <= MaximumError
}
