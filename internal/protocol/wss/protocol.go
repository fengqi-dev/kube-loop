package wss

import (
	"errors"
	"strings"
	"time"
)

const (
	Version                     = "2.0"
	Subprotocol                 = "kubeloop-mux-v2"
	VersionHeader               = "KubeLoop-WSS-Version"
	CapabilityTunnelControl     = "tunnel.control.v2"
	CapabilityTrafficWebSocket  = "traffic.websocket.v1"
	CapabilityTrafficEncryption = "traffic.noise.v1"
	MaximumHandshakeBytes       = 8 << 10
	DefaultHandshakeTimeout     = 10 * time.Second

	KindClientHello = "client_hello"
	KindServerHello = "server_hello"
	KindReject      = "reject"

	CodeVersionMismatch          = "VERSION_MISMATCH"
	CodeClientVersionUnsupported = "CLIENT_VERSION_UNSUPPORTED"
	CodeDeviceMismatch           = "DEVICE_MISMATCH"
	CodeInvalidHandshake         = "INVALID_HANDSHAKE"
	CodeEncryptionMismatch       = "ENCRYPTION_MISMATCH"
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

const developmentVersion = "dev"

func NewClientHello(clientVersion, deviceID string) ClientHello {
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = developmentVersion
	}
	return ClientHello{
		Type:             KindClientHello,
		ProtocolVersions: []string{Version},
		ClientVersion:    clientVersion,
		DeviceID:         deviceID,
		Capabilities: []string{
			"cancel",
			"half-close",
			"ping-pong",
			"smux.v2",
			CapabilityTrafficWebSocket,
			CapabilityTrafficEncryption,
			CapabilityTunnelControl,
		},
	}
}

func NewServerHello(serverVersion string, limits Limits) ServerHello {
	if strings.TrimSpace(serverVersion) == "" {
		serverVersion = developmentVersion
	}
	return ServerHello{
		Type:            KindServerHello,
		ProtocolVersion: Version,
		ServerVersion:   serverVersion,
		Capabilities: []string{
			"cancel",
			"half-close",
			"ping-pong",
			"smux.v2",
			CapabilityTrafficWebSocket,
			CapabilityTrafficEncryption,
			CapabilityTunnelControl,
		},
		Limits: limits,
	}
}

func NewReject(code, message string, versions ...string) Reject {
	return Reject{Type: KindReject, Code: code, Message: message, SupportedVersions: append([]string(nil), versions...)}
}
