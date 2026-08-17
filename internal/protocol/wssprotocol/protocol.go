package wssprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	version "github.com/hashicorp/go-version"
)

const (
	Version                    = "2.0"
	Subprotocol                = "kubeloop-mux-v2"
	VersionHeader              = "KubeLoop-WSS-Version"
	CapabilityTrafficWebSocket = "traffic.websocket.v1"
	MaximumHandshakeBytes      = 8 << 10
	DefaultHandshakeTimeout    = 10 * time.Second

	KindClientHello = "client_hello"
	KindServerHello = "server_hello"
	KindReject      = "reject"

	CodeVersionMismatch          = "VERSION_MISMATCH"
	CodeClientVersionUnsupported = "CLIENT_VERSION_UNSUPPORTED"
	CodeDeviceMismatch           = "DEVICE_MISMATCH"
	CodeInvalidHandshake         = "INVALID_HANDSHAKE"
	CodeUserCapacityExceeded     = "USER_CAPACITY_EXCEEDED"
)

var (
	ErrInvalidHandshake = errors.New("invalid WSS v2 handshake")
)

type ClientHello struct {
	Type             string   `json:"type"`
	ProtocolVersions []string `json:"protocolVersions"`
	ClientVersion    string   `json:"clientVersion"`
	DeviceID         string   `json:"deviceId"`
	Capabilities     []string `json:"capabilities"`
}

type Limits struct {
	MaximumFrameBytes           int64 `json:"maximumFrameBytes"`
	MaximumStreamFrameBytes     int   `json:"maximumStreamFrameBytes"`
	MaximumStreamsPerConnection int   `json:"maximumStreamsPerConnection"`
	MaximumPhysicalConnections  int   `json:"maximumPhysicalConnections"`
	MaximumConnectionsPerUser   int   `json:"maximumConnectionsPerUser"`
	StreamIdleTimeoutMillis     int64 `json:"streamIdleTimeoutMs"`
}

type ServerHello struct {
	Type            string   `json:"type"`
	ProtocolVersion string   `json:"protocolVersion"`
	ServerVersion   string   `json:"serverVersion"`
	Capabilities    []string `json:"capabilities"`
	Limits          Limits   `json:"limits"`
}

type Reject struct {
	Type              string   `json:"type"`
	Code              string   `json:"code"`
	Message           string   `json:"message"`
	SupportedVersions []string `json:"supportedVersions,omitempty"`
}

type Message struct {
	ClientHello *ClientHello
	ServerHello *ServerHello
	Reject      *Reject
}

func NewClientHello(clientVersion, deviceID string) ClientHello {
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = "dev"
	}
	return ClientHello{
		Type: KindClientHello, ProtocolVersions: []string{Version},
		ClientVersion: clientVersion, DeviceID: deviceID,
		Capabilities: []string{"cancel", "half-close", "ping-pong", "smux.v2", CapabilityTrafficWebSocket, "tunnel.open.v2"},
	}
}

func NewServerHello(serverVersion string, limits Limits) ServerHello {
	if strings.TrimSpace(serverVersion) == "" {
		serverVersion = "dev"
	}
	return ServerHello{
		Type: KindServerHello, ProtocolVersion: Version, ServerVersion: serverVersion,
		Capabilities: []string{"cancel", "half-close", "ping-pong", "smux.v2", CapabilityTrafficWebSocket, "tunnel.open.v2"},
		Limits:       limits,
	}
}

func NewReject(code, message string, versions ...string) Reject {
	return Reject{Type: KindReject, Code: code, Message: message, SupportedVersions: append([]string(nil), versions...)}
}

func Encode(message any) ([]byte, error) {
	if err := validateMessage(message); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(message)
	if err != nil || len(raw) > MaximumHandshakeBytes {
		return nil, ErrInvalidHandshake
	}
	return raw, nil
}

func Decode(raw []byte) (Message, error) {
	if len(raw) == 0 || len(raw) > MaximumHandshakeBytes {
		return Message{}, ErrInvalidHandshake
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Message{}, ErrInvalidHandshake
	}
	switch envelope.Type {
	case KindClientHello:
		var hello ClientHello
		if err := decodeStrict(raw, &hello); err != nil || hello.Validate() != nil {
			return Message{}, ErrInvalidHandshake
		}
		return Message{ClientHello: &hello}, nil
	case KindServerHello:
		var hello ServerHello
		if err := decodeStrict(raw, &hello); err != nil || hello.Validate() != nil {
			return Message{}, ErrInvalidHandshake
		}
		return Message{ServerHello: &hello}, nil
	case KindReject:
		var reject Reject
		if err := decodeStrict(raw, &reject); err != nil || reject.Validate() != nil {
			return Message{}, ErrInvalidHandshake
		}
		return Message{Reject: &reject}, nil
	default:
		return Message{}, ErrInvalidHandshake
	}
}

func Read(ctx context.Context, connection *websocket.Conn) (Message, error) {
	if connection == nil {
		return Message{}, ErrInvalidHandshake
	}
	messageType, raw, err := connection.Read(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("read WSS handshake message: %w", err)
	}
	if messageType != websocket.MessageBinary {
		return Message{}, ErrInvalidHandshake
	}
	return Decode(raw)
}

func Write(ctx context.Context, connection *websocket.Conn, message any) error {
	raw, err := Encode(message)
	if err != nil {
		return err
	}
	if err := connection.Write(ctx, websocket.MessageBinary, raw); err != nil {
		return fmt.Errorf("write WSS handshake: %w", err)
	}
	return nil
}

func (hello ClientHello) Validate() error {
	if hello.Type != KindClientHello || len(hello.ProtocolVersions) == 0 || len(hello.ProtocolVersions) > 8 ||
		!safeText(hello.ClientVersion, 64) || !safeIdentifier(hello.DeviceID, 256) ||
		!validUniqueIdentifiers(hello.Capabilities, 32, 64) {
		return ErrInvalidHandshake
	}
	if !validUniqueIdentifiers(hello.ProtocolVersions, 8, 16) {
		return ErrInvalidHandshake
	}
	return nil
}

func (hello ServerHello) Validate() error {
	if hello.Type != KindServerHello || !safeIdentifier(hello.ProtocolVersion, 16) ||
		!safeText(hello.ServerVersion, 64) || !validUniqueIdentifiers(hello.Capabilities, 32, 64) ||
		hello.Limits.MaximumFrameBytes < MaximumHandshakeBytes || hello.Limits.MaximumFrameBytes > 16<<20 ||
		hello.Limits.MaximumStreamFrameBytes < 1 || hello.Limits.MaximumStreamFrameBytes > 1<<20 ||
		hello.Limits.MaximumStreamsPerConnection < 1 || hello.Limits.MaximumStreamsPerConnection > 1<<20 ||
		hello.Limits.MaximumPhysicalConnections < 1 || hello.Limits.MaximumPhysicalConnections > 1<<20 ||
		hello.Limits.MaximumConnectionsPerUser < 1 || hello.Limits.MaximumConnectionsPerUser > hello.Limits.MaximumPhysicalConnections ||
		hello.Limits.StreamIdleTimeoutMillis <= 0 || hello.Limits.StreamIdleTimeoutMillis > (24*time.Hour).Milliseconds() {
		return ErrInvalidHandshake
	}
	return nil
}

func (reject Reject) Validate() error {
	if reject.Type != KindReject || !safeIdentifier(reject.Code, 64) || !safeText(reject.Message, 256) ||
		(len(reject.SupportedVersions) > 0 && !validUniqueIdentifiers(reject.SupportedVersions, 8, 16)) {
		return ErrInvalidHandshake
	}
	return nil
}

func Negotiate(client, server []string) (string, error) {
	if !validUniqueIdentifiers(client, 8, 16) || !validUniqueIdentifiers(server, 8, 16) {
		return "", ErrInvalidHandshake
	}
	for _, supported := range server {
		if slices.Contains(client, supported) {
			return supported, nil
		}
	}
	return "", errors.New(CodeVersionMismatch)
}

func CheckClientVersion(current, minimum string) error {
	current = strings.TrimSpace(current)
	minimum = strings.TrimSpace(minimum)
	if minimum == "" || current == "dev" {
		return nil
	}
	currentVersion, currentErr := version.NewVersion(strings.TrimPrefix(current, "v"))
	minimumVersion, minimumErr := version.NewVersion(strings.TrimPrefix(minimum, "v"))
	if currentErr != nil || minimumErr != nil || currentVersion.LessThan(minimumVersion) {
		return errors.New(CodeClientVersionUnsupported)
	}
	return nil
}

func validateMessage(message any) error {
	switch value := message.(type) {
	case ClientHello:
		return value.Validate()
	case *ClientHello:
		if value == nil {
			return ErrInvalidHandshake
		}
		return value.Validate()
	case ServerHello:
		return value.Validate()
	case *ServerHello:
		if value == nil {
			return ErrInvalidHandshake
		}
		return value.Validate()
	case Reject:
		return value.Validate()
	case *Reject:
		if value == nil {
			return ErrInvalidHandshake
		}
		return value.Validate()
	default:
		return ErrInvalidHandshake
	}
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidHandshake
	}
	return nil
}

func validUniqueIdentifiers(values []string, maximumItems, maximumLength int) bool {
	if len(values) == 0 || len(values) > maximumItems {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !safeIdentifier(value, maximumLength) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func safeText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func safeIdentifier(value string, maximum int) bool {
	if !safeText(value, maximum) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}
