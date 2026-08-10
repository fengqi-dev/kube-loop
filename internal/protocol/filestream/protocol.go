package filestream

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	Data     byte = 1
	Complete byte = 2
	Progress byte = 3
	Result   byte = 4
	Cancel   byte = 5

	ResultSucceeded byte = 0
	ResultFailed    byte = 1
	ResultCancelled byte = 2

	MaximumData  = 256 << 10
	MaximumError = 4096
	resultHeader = 41
)

type Frame struct {
	Type    byte
	Payload []byte
}

type ProgressStatus struct {
	Transferred uint64
	Total       uint64
}

type TransferResult struct {
	Status      byte
	Transferred uint64
	Checksum    [32]byte
	HasChecksum bool
	Error       string
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
		return Frame{}, errors.New("file transfer frame is empty")
	}
	frame := Frame{Type: encoded[0], Payload: append([]byte(nil), encoded[1:]...)}
	if err := validate(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func EncodeProgress(status ProgressStatus) ([]byte, error) {
	if status.Total != 0 && status.Transferred > status.Total {
		return nil, errors.New("file transfer progress is invalid")
	}
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[0:8], status.Transferred)
	binary.BigEndian.PutUint64(payload[8:16], status.Total)
	return Encode(Frame{Type: Progress, Payload: payload})
}

func DecodeProgress(frame Frame) (ProgressStatus, error) {
	if frame.Type != Progress || len(frame.Payload) != 16 {
		return ProgressStatus{}, errors.New("invalid file transfer progress frame")
	}
	status := ProgressStatus{
		Transferred: binary.BigEndian.Uint64(frame.Payload[0:8]),
		Total:       binary.BigEndian.Uint64(frame.Payload[8:16]),
	}
	if status.Total != 0 && status.Transferred > status.Total {
		return ProgressStatus{}, errors.New("file transfer progress is invalid")
	}
	return status, nil
}

func EncodeResult(result TransferResult) ([]byte, error) {
	if result.Status > ResultCancelled {
		return nil, errors.New("file transfer result status is invalid")
	}
	if len(result.Error) > MaximumError {
		result.Error = result.Error[:MaximumError]
	}
	if result.Status == ResultSucceeded && result.Error != "" {
		return nil, errors.New("successful file transfer result cannot contain an error")
	}
	payload := make([]byte, resultHeader+len(result.Error))
	payload[0] = result.Status
	binary.BigEndian.PutUint64(payload[1:9], result.Transferred)
	if result.HasChecksum {
		copy(payload[9:41], result.Checksum[:])
	}
	copy(payload[resultHeader:], result.Error)
	return Encode(Frame{Type: Result, Payload: payload})
}

func DecodeResult(frame Frame) (TransferResult, error) {
	if frame.Type != Result || len(frame.Payload) < resultHeader || len(frame.Payload)-resultHeader > MaximumError {
		return TransferResult{}, errors.New("invalid file transfer result frame")
	}
	result := TransferResult{
		Status: frame.Payload[0], Transferred: binary.BigEndian.Uint64(frame.Payload[1:9]),
		Error: string(frame.Payload[resultHeader:]),
	}
	if result.Status > ResultCancelled || (result.Status == ResultSucceeded && result.Error != "") {
		return TransferResult{}, errors.New("file transfer result status is invalid")
	}
	copy(result.Checksum[:], frame.Payload[9:41])
	for _, value := range result.Checksum {
		if value != 0 {
			result.HasChecksum = true
			break
		}
	}
	return result, nil
}

func ParseChecksum(value string) ([32]byte, error) {
	var checksum [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(checksum) {
		return checksum, errors.New("SHA-256 checksum must contain 64 hexadecimal characters")
	}
	copy(checksum[:], decoded)
	return checksum, nil
}

func FormatChecksum(checksum [32]byte) string { return hex.EncodeToString(checksum[:]) }

func validate(frame Frame) error {
	switch frame.Type {
	case Data:
		if len(frame.Payload) == 0 || len(frame.Payload) > MaximumData {
			return fmt.Errorf("file transfer data frame must contain 1 to %d bytes", MaximumData)
		}
	case Complete, Cancel:
		if len(frame.Payload) != 0 {
			return errors.New("file transfer control frame must be empty")
		}
	case Progress:
		if len(frame.Payload) != 16 {
			return errors.New("file transfer progress payload must be 16 bytes")
		}
	case Result:
		if len(frame.Payload) < resultHeader || len(frame.Payload)-resultHeader > MaximumError {
			return errors.New("file transfer result payload is invalid")
		}
	default:
		return fmt.Errorf("unsupported file transfer frame type %d", frame.Type)
	}
	return nil
}
