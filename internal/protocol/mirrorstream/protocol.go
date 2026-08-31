package mirrorstream

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

	MaximumData  = 256 << 10
	MaximumError = 4096
	HeaderSize   = 14
)

// Frame multiplexes shadow request copies. ServicePort is the authoritative
// Service port selected by the user; primary backend and listener addresses
// are intentionally absent from the desktop protocol.
type Frame struct {
	Type        byte
	StreamID    uint64
	ServicePort uint32
	Protocol    byte
	Payload     []byte
}

func Encode(frame Frame) ([]byte, error) {
	if err := validate(frame); err != nil {
		return nil, err
	}
	encoded := make([]byte, HeaderSize+len(frame.Payload))
	encoded[0] = frame.Type
	binary.BigEndian.PutUint64(encoded[1:9], frame.StreamID)
	binary.BigEndian.PutUint32(encoded[9:13], frame.ServicePort)
	encoded[13] = frame.Protocol
	copy(encoded[HeaderSize:], frame.Payload)
	return encoded, nil
}

func Decode(encoded []byte) (Frame, error) {
	if len(encoded) < HeaderSize {
		return Frame{}, errors.New("mirror frame is truncated")
	}
	frame := Frame{
		Type: encoded[0], StreamID: binary.BigEndian.Uint64(encoded[1:9]),
		ServicePort: binary.BigEndian.Uint32(encoded[9:13]), Protocol: encoded[13],
		Payload: append([]byte(nil), encoded[HeaderSize:]...),
	}
	if err := validate(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func validate(frame Frame) error {
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
			frame.Protocol == ProtocolUDP && len(frame.Payload) > 0 && len(frame.Payload) <= 65507, "datagram"
	case Stop:
		valid, name = validTerminalFrame(frame, false), "stop"
	default:
		return fmt.Errorf("unsupported mirror frame type %d", frame.Type)
	}
	if !valid {
		return fmt.Errorf("mirror %s frame is invalid", name)
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
