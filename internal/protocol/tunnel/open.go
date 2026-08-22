package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/google/uuid"
)

type OpenRequest struct {
	Command byte
	Host    string
	Port    uint16
}

// TrafficOpenRequest identifies one reverse traffic Task carried by this
// logical stream. TaskID is always the canonical, lowercase UUID form.
type TrafficOpenRequest struct {
	Mode   string
	TaskID string
}

func (r OpenRequest) Address() string {
	return net.JoinHostPort(r.Host, strconv.Itoa(int(r.Port)))
}

func WriteOpen(w io.Writer, request OpenRequest, token SessionToken) error {
	if request.Command != CommandTCP && request.Command != CommandUDP {
		return fmt.Errorf("unsupported command %d", request.Command)
	}
	if request.Host == "" || len(request.Host) > maxHostSize {
		return errors.New("target host length is invalid")
	}
	if request.Port == 0 {
		return errors.New("target port is required")
	}
	value, err := appendSessionHeader(make([]byte, 0, 41+len(request.Host)), request.Command, token)
	if err != nil {
		return err
	}
	value = appendUint16String(value, request.Host)
	value = binary.BigEndian.AppendUint16(value, request.Port)
	return writeAll(w, value)
}

func ReadOpen(r io.Reader) (OpenRequest, error) {
	header, err := ReadSessionHeader(r)
	if err != nil {
		return OpenRequest{}, err
	}
	return ReadOpenBody(r, header.Command)
}

// ReadOpenBody reads host/port after magic+command were already consumed.
func ReadOpenBody(r io.Reader, command byte) (OpenRequest, error) {
	if command != CommandTCP && command != CommandUDP {
		return OpenRequest{}, fmt.Errorf("unsupported command %d", command)
	}
	var sizeBuf [2]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return OpenRequest{}, err
	}
	hostSize := int(binary.BigEndian.Uint16(sizeBuf[:]))
	if hostSize == 0 || hostSize > maxHostSize {
		return OpenRequest{}, errors.New("target host length is invalid")
	}
	target := make([]byte, hostSize+2)
	if _, err := io.ReadFull(r, target); err != nil {
		return OpenRequest{}, err
	}
	port := binary.BigEndian.Uint16(target[hostSize:])
	if port == 0 {
		return OpenRequest{}, errors.New("target port is required")
	}
	return OpenRequest{Command: command, Host: string(target[:hostSize]), Port: port}, nil
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
	return writeAll(w, value)
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

func appendUint16String(value []byte, text string) []byte {
	// Callers validate protocol strings against their uint16 wire limit.
	value = binary.BigEndian.AppendUint16(value, uint16(len(text))) //nolint:gosec // Wire strings are bounded.
	return append(value, text...)
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
