package tunnel

import (
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

// TrafficOpenRequest identifies one reverse traffic Task carried by this
// logical stream. TaskID is always the canonical, lowercase UUID form.
type TrafficOpenRequest struct {
	Mode   string
	TaskID string
}

// WriteTrafficOpen opens an Exchange, Mirror, or Preview Task on an existing
// tunnel multiplexer connection.
func WriteTrafficOpen(w io.Writer, request TrafficOpenRequest, token SessionToken) error {
	mode, err := encodeTrafficMode(request.Mode)
	if err != nil {
		return err
	}
	if err := validateCanonicalTaskID(request.TaskID); err != nil {
		return err
	}
	value, err := appendSessionHeader(make([]byte, 0, 5+len(token)+trafficOpenBodySize), CommandTraffic, token)
	if err != nil {
		return err
	}
	value = append(value, mode)
	value = append(value, request.TaskID...)
	return utils.WriteAll(w, value)
}

func ReadTrafficOpen(r io.Reader) (TrafficOpenRequest, error) {
	header, err := ReadSessionHeader(r)
	if err != nil {
		return TrafficOpenRequest{}, err
	}
	if header.Command != CommandTraffic {
		return TrafficOpenRequest{}, fmt.Errorf("unsupported command %d", header.Command)
	}
	return ReadTrafficOpenBody(r)
}

// ReadTrafficOpenBody reads the fixed-size Task selector after the tunnel
// session header was already consumed.
func ReadTrafficOpenBody(r io.Reader) (TrafficOpenRequest, error) {
	var body [trafficOpenBodySize]byte
	if _, err := io.ReadFull(r, body[:]); err != nil {
		return TrafficOpenRequest{}, err
	}
	mode, err := decodeTrafficMode(body[0])
	if err != nil {
		return TrafficOpenRequest{}, err
	}
	taskID := string(body[1:])
	if err := validateCanonicalTaskID(taskID); err != nil {
		return TrafficOpenRequest{}, err
	}
	return TrafficOpenRequest{Mode: mode, TaskID: taskID}, nil
}

func encodeTrafficMode(mode string) (byte, error) {
	switch mode {
	case TrafficModeExchange:
		return trafficModeExchange, nil
	case TrafficModeMirror:
		return trafficModeMirror, nil
	case TrafficModePreview:
		return trafficModePreview, nil
	default:
		return 0, errors.New("traffic mode is invalid")
	}
}

func decodeTrafficMode(mode byte) (string, error) {
	switch mode {
	case trafficModeExchange:
		return TrafficModeExchange, nil
	case trafficModeMirror:
		return TrafficModeMirror, nil
	case trafficModePreview:
		return TrafficModePreview, nil
	default:
		return "", errors.New("traffic mode is invalid")
	}
}

func validateCanonicalTaskID(taskID string) error {
	parsed, err := uuid.Parse(taskID)
	if err != nil || parsed.String() != taskID {
		return errors.New("traffic Task ID must be a canonical UUID")
	}
	return nil
}
