package tunnel

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
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

var magic = [4]byte{'K', 'C', 'G', 2}

// SessionToken is an unguessable capability shared by all Gateway
// connections that belong to one desktop session.
type SessionToken [32]byte

type SessionHeader struct {
	Command byte
	Token   SessionToken
}

func NewSessionToken() (SessionToken, error) {
	var token SessionToken
	if _, err := rand.Read(token[:]); err != nil {
		return SessionToken{}, fmt.Errorf("generate Gateway session token: %w", err)
	}
	return token, nil
}

// RelaySessionToken deterministically maps one authenticated Cluster Session
// generation to the protocol tenant key. It is not a credential: authorization
// is provided by the RelayTicket on the enclosing WebSocket connection.
func RelaySessionToken(sessionID string, generation uint64) (SessionToken, error) {
	sessionID = strings.TrimSpace(sessionID)
	parsed, err := uuid.Parse(sessionID)
	if err != nil || parsed.String() != sessionID {
		return SessionToken{}, errors.New("Relay session ID must be a canonical UUID")
	}
	if generation == 0 {
		return SessionToken{}, errors.New("Relay session generation is required")
	}
	digest := sha256.Sum256([]byte("kubeloop-relay-session-v2\x00" + sessionID + "\x00" + strconv.FormatUint(generation, 10)))
	return SessionToken(digest), nil
}

type OpenRequest struct {
	Command byte
	Host    string
	Port    uint16
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

// ReadSessionHeader reads the protocol marker, command, and tenant capability.
func ReadSessionHeader(r io.Reader) (SessionHeader, error) {
	var header [5 + len(SessionToken{})]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return SessionHeader{}, err
	}
	if [4]byte(header[:4]) != magic {
		return SessionHeader{}, errors.New("invalid tunnel protocol magic")
	}
	token := SessionToken(header[5:])
	if token == (SessionToken{}) {
		return SessionHeader{}, errors.New("Gateway session token is required")
	}
	return SessionHeader{Command: header[4], Token: token}, nil
}

// WriteAuthorizedControlSession registers the immutable NetworkSpec snapshot
// for a RelayTicket-bound Session.
func WriteAuthorizedControlSession(w io.Writer, token SessionToken, spec networkspec.Spec) error {
	contents, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		return err
	}
	value, err := appendSessionHeader(make([]byte, 0, 41+4+len(contents)), CommandControl, token)
	if err != nil {
		return err
	}
	value = binary.BigEndian.AppendUint32(value, uint32(len(contents)))
	value = append(value, contents...)
	return writeAll(w, value)
}

func ReadAuthorizedControlSpec(r io.Reader) (networkspec.Spec, error) {
	var sizeRaw [4]byte
	if _, err := io.ReadFull(r, sizeRaw[:]); err != nil {
		return networkspec.Spec{}, err
	}
	size := int(binary.BigEndian.Uint32(sizeRaw[:]))
	if size == 0 || size > networkspec.MaximumJSONSize {
		return networkspec.Spec{}, errors.New("authorized NetworkSpec size is invalid")
	}
	contents := make([]byte, size)
	if _, err := io.ReadFull(r, contents); err != nil {
		return networkspec.Spec{}, err
	}
	return networkspec.Decode(contents)
}

func WriteAccept(w io.Writer, streamID uint64, token SessionToken) error {
	value, err := appendSessionHeader(make([]byte, 0, 45), CommandAccept, token)
	if err != nil {
		return err
	}
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

func appendSessionHeader(value []byte, command byte, token SessionToken) ([]byte, error) {
	if token == (SessionToken{}) {
		return nil, errors.New("Gateway session token is required")
	}
	value = append(value, magic[:]...)
	value = append(value, command)
	return append(value, token[:]...), nil
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
