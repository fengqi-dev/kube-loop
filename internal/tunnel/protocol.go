package tunnel

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

const (
	CommandTCP     byte = 1
	CommandUDP     byte = 2
	CommandControl byte = 3
	CommandAccept  byte = 4

	StatusOK    byte = 0
	StatusError byte = 1

	MaxDatagramSize = 65507
	maxHostSize     = 1024
	maxErrorSize    = 4096
	maxIDSize       = 256
)

var magic = [4]byte{'K', 'C', 'G', 1}

type OpenRequest struct {
	Command byte
	Host    string
	Port    uint16
}

func (r OpenRequest) Address() string {
	return net.JoinHostPort(r.Host, strconv.Itoa(int(r.Port)))
}

func WriteOpen(w io.Writer, request OpenRequest) error {
	if request.Command != CommandTCP && request.Command != CommandUDP {
		return fmt.Errorf("unsupported command %d", request.Command)
	}
	if request.Host == "" || len(request.Host) > maxHostSize {
		return errors.New("target host length is invalid")
	}
	if request.Port == 0 {
		return errors.New("target port is required")
	}
	value := appendSessionHeader(make([]byte, 0, 9+len(request.Host)), request.Command)
	value = appendUint16String(value, request.Host)
	value = binary.BigEndian.AppendUint16(value, request.Port)
	return writeAll(w, value)
}

func ReadOpen(r io.Reader) (OpenRequest, error) {
	command, err := ReadSessionHeader(r)
	if err != nil {
		return OpenRequest{}, err
	}
	return ReadOpenBody(r, command)
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

// ReadSessionHeader reads the shared magic + command byte used by all session types.
func ReadSessionHeader(r io.Reader) (command byte, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, err
	}
	if [4]byte(header[:4]) != magic {
		return 0, errors.New("invalid tunnel protocol magic")
	}
	return header[4], nil
}

func WriteControlSession(w io.Writer) error {
	return writeAll(w, appendSessionHeader(nil, CommandControl))
}

func WriteAccept(w io.Writer, streamID uint64) error {
	value := appendSessionHeader(make([]byte, 0, 13), CommandAccept)
	return writeAll(w, binary.BigEndian.AppendUint64(value, streamID))
}

func ReadAcceptStreamID(r io.Reader) (uint64, error) {
	var raw [8]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw[:]), nil
}

func WriteStatus(w io.Writer, err error) error {
	if err == nil {
		return writeAll(w, []byte{StatusOK})
	}
	message := err.Error()
	if len(message) > maxErrorSize {
		message = message[:maxErrorSize]
	}
	value := binary.BigEndian.AppendUint16([]byte{StatusError}, uint16(len(message)))
	value = append(value, message...)
	return writeAll(w, value)
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

func WriteDatagram(w io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxDatagramSize {
		return fmt.Errorf("invalid datagram size %d", len(payload))
	}
	var size [2]byte
	binary.BigEndian.PutUint16(size[:], uint16(len(payload)))
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

func appendSessionHeader(value []byte, command byte) []byte {
	value = append(value, magic[:]...)
	return append(value, command)
}

func appendUint16String(value []byte, text string) []byte {
	value = binary.BigEndian.AppendUint16(value, uint16(len(text)))
	return append(value, text...)
}

func writeAll(w io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := w.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}
