package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func WriteStatus(w io.Writer, err error) error {
	if err == nil {
		return utils.WriteAll(w, []byte{StatusOK})
	}
	message := err.Error()
	if len(message) > maxErrorSize {
		message = message[:maxErrorSize]
	}
	// message is truncated to the uint16-sized maxErrorSize above.
	messageLength := uint16(len(message)) //nolint:gosec // Message is truncated to a uint16-sized limit.
	value := binary.BigEndian.AppendUint16([]byte{StatusError}, messageLength)
	value = append(value, message...)
	return utils.WriteAll(w, value)
}

func ReadStatus(r io.Reader) error {
	var status [1]byte
	if _, err := io.ReadFull(r, status[:]); err != nil {
		return err
	}
	switch status[0] {
	case StatusOK:
		return nil
	case StatusError:
		var size [2]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return err
		}
		messageSize := int(binary.BigEndian.Uint16(size[:]))
		if messageSize > maxErrorSize {
			return errors.New("gateway error message is too large")
		}
		message := make([]byte, messageSize)
		if _, err := io.ReadFull(r, message); err != nil {
			return err
		}
		return errors.New(string(message))
	default:
		return fmt.Errorf("invalid gateway status %d", status[0])
	}
}
