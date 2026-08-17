package execstream

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	Stdin      byte = 1
	Stdout     byte = 2
	Stderr     byte = 3
	Resize     byte = 4
	CloseStdin byte = 5
	Exit       byte = 6

	MaximumPayload = 1 << 20
	MaximumError   = 4096
)

type Frame struct {
	Type    byte
	Payload []byte
}

type TerminalSize struct {
	Width  uint16
	Height uint16
}

type ExitStatus struct {
	Code      uint32
	Cancelled bool
	Error     string
}

func Encode(frame Frame) ([]byte, error) {
	if err := validate(frame); err != nil {
		return nil, err
	}
	encoded := make([]byte, len(frame.Payload)+1)
	encoded[0] = frame.Type
	copy(encoded[1:], frame.Payload)
	return encoded, nil
}

func Decode(encoded []byte) (Frame, error) {
	if len(encoded) == 0 {
		return Frame{}, errors.New("exec frame is empty")
	}
	frame := Frame{Type: encoded[0], Payload: append([]byte(nil), encoded[1:]...)}
	if err := validate(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func EncodeResize(size TerminalSize) ([]byte, error) {
	if size.Width == 0 || size.Height == 0 {
		return nil, errors.New("terminal width and height are required")
	}
	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[0:2], size.Width)
	binary.BigEndian.PutUint16(payload[2:4], size.Height)
	return Encode(Frame{Type: Resize, Payload: payload})
}

func DecodeResize(frame Frame) (TerminalSize, error) {
	if frame.Type != Resize || len(frame.Payload) != 4 {
		return TerminalSize{}, errors.New("invalid terminal resize frame")
	}
	size := TerminalSize{
		Width: binary.BigEndian.Uint16(frame.Payload[0:2]), Height: binary.BigEndian.Uint16(frame.Payload[2:4]),
	}
	if size.Width == 0 || size.Height == 0 {
		return TerminalSize{}, errors.New("terminal width and height are required")
	}
	return size, nil
}

func EncodeExit(status ExitStatus) ([]byte, error) {
	if len(status.Error) > MaximumError {
		status.Error = status.Error[:MaximumError]
	}
	payload := make([]byte, 5+len(status.Error))
	binary.BigEndian.PutUint32(payload[0:4], status.Code)
	if status.Cancelled {
		payload[4] = 1
	}
	copy(payload[5:], status.Error)
	return Encode(Frame{Type: Exit, Payload: payload})
}

func DecodeExit(frame Frame) (ExitStatus, error) {
	if frame.Type != Exit || len(frame.Payload) < 5 || len(frame.Payload)-5 > MaximumError || frame.Payload[4] > 1 {
		return ExitStatus{}, errors.New("invalid exec exit frame")
	}
	return ExitStatus{
		Code: binary.BigEndian.Uint32(frame.Payload[0:4]), Cancelled: frame.Payload[4] == 1,
		Error: string(frame.Payload[5:]),
	}, nil
}

func validate(frame Frame) error {
	if len(frame.Payload) > MaximumPayload {
		return errors.New("exec frame exceeds 1 MiB")
	}
	switch frame.Type {
	case Stdin, Stdout, Stderr:
		if len(frame.Payload) == 0 {
			return errors.New("exec data frame is empty")
		}
	case Resize:
		if len(frame.Payload) != 4 {
			return errors.New("terminal resize payload must be four bytes")
		}
	case CloseStdin:
		if len(frame.Payload) != 0 {
			return errors.New("close-stdin frame must be empty")
		}
	case Exit:
		if len(frame.Payload) < 5 {
			return errors.New("exec exit payload is incomplete")
		}
	default:
		return fmt.Errorf("unsupported exec frame type %d", frame.Type)
	}
	return nil
}
