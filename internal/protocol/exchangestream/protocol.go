package exchangestream

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
	headerSize   = 14
)

// Frame multiplexes reverse Service traffic over one authenticated WebSocket.
// ServicePort is the stable Service port selected by the user; the Control Plane's
// ephemeral listener port never crosses the desktop trust boundary.
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
	encoded := make([]byte, headerSize+len(frame.Payload))
	encoded[0] = frame.Type
	binary.BigEndian.PutUint64(encoded[1:9], frame.StreamID)
	binary.BigEndian.PutUint32(encoded[9:13], frame.ServicePort)
	encoded[13] = frame.Protocol
	copy(encoded[headerSize:], frame.Payload)
	return encoded, nil
}

func Decode(encoded []byte) (Frame, error) {
	if len(encoded) < headerSize {
		return Frame{}, errors.New("Exchange frame is truncated")
	}
	frame := Frame{
		Type:        encoded[0],
		StreamID:    binary.BigEndian.Uint64(encoded[1:9]),
		ServicePort: binary.BigEndian.Uint32(encoded[9:13]),
		Protocol:    encoded[13],
		Payload:     append([]byte(nil), encoded[headerSize:]...),
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
			return errors.New("Exchange ready frame is invalid")
		}
	case Open:
		if frame.StreamID == 0 || frame.ServicePort == 0 || frame.Protocol != ProtocolTCP || len(frame.Payload) != 0 {
			return errors.New("Exchange open frame is invalid")
		}
	case Data:
		if frame.StreamID == 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) == 0 || len(frame.Payload) > MaximumData {
			return errors.New("Exchange data frame is invalid")
		}
	case CloseWrite:
		if frame.StreamID == 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) != 0 {
			return errors.New("Exchange half-close frame is invalid")
		}
	case Close:
		if frame.StreamID == 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) > MaximumError {
			return errors.New("Exchange close frame is invalid")
		}
	case Datagram:
		if frame.StreamID == 0 || frame.ServicePort == 0 || frame.Protocol != ProtocolUDP || len(frame.Payload) == 0 || len(frame.Payload) > 65507 {
			return errors.New("Exchange datagram frame is invalid")
		}
	case Stop:
		if frame.StreamID != 0 || frame.ServicePort != 0 || frame.Protocol != 0 || len(frame.Payload) > MaximumError {
			return errors.New("Exchange stop frame is invalid")
		}
	default:
		return fmt.Errorf("unsupported Exchange frame type %d", frame.Type)
	}
	return nil
}
