// Package mirrorstream defines the neutral, authenticated reverse stream used
// to deliver best-effort request copies to a desktop shadow target. Primary
// Service responses never travel through this protocol.
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
		return Frame{}, errors.New("Mirror frame is truncated")
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
	switch frame.Type {
	case Ready:
		if frame.StreamID != 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) != 0 {
			return errors.New("Mirror ready frame is invalid")
		}
	case Open:
		if frame.StreamID == 0 || frame.ServicePort == 0 || frame.Protocol != ProtocolTCP || len(frame.Payload) != 0 {
			return errors.New("Mirror open frame is invalid")
		}
	case Data:
		if frame.StreamID == 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) == 0 || len(frame.Payload) > MaximumData {
			return errors.New("Mirror data frame is invalid")
		}
	case CloseWrite:
		if frame.StreamID == 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) != 0 {
			return errors.New("Mirror half-close frame is invalid")
		}
	case Close:
		if frame.StreamID == 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) > MaximumError {
			return errors.New("Mirror close frame is invalid")
		}
	case Datagram:
		if frame.StreamID == 0 || frame.ServicePort == 0 || frame.Protocol != ProtocolUDP || len(frame.Payload) == 0 || len(frame.Payload) > 65507 {
			return errors.New("Mirror datagram frame is invalid")
		}
	case Stop:
		if frame.StreamID != 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) > MaximumError {
			return errors.New("Mirror stop frame is invalid")
		}
	default:
		return fmt.Errorf("unsupported Mirror frame type %d", frame.Type)
	}
	return nil
}
