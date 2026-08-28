package trafficinspect

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const EventSchemaVersion = 1

type Protocol string

const (
	ProtocolUnknown Protocol = ""
	ProtocolHTTP    Protocol = "http"
	ProtocolHTTPS   Protocol = "https"
	ProtocolGRPC    Protocol = "grpc"
	ProtocolGRPCS   Protocol = "grpcs"
)

type EventType string

const (
	EventTypeUnknown  EventType = ""
	EventTypeRequest  EventType = "request"
	EventTypeResponse EventType = "response"
	EventTypeBody     EventType = "body"
)

// Event is the stable JSON-serializable traffic inspection output contract.
type Event struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"event_id"`
	FlowID        string         `json:"flow_id"`
	Timestamp     time.Time      `json:"timestamp"`
	Type          EventType      `json:"type"`
	Protocol      Protocol       `json:"protocol"`
	TLS           bool           `json:"tls"`
	Destination   string         `json:"destination"`
	Duration      int64          `json:"duration_ms,omitempty"`
	HTTP          *HTTPEvent     `json:"http,omitempty"`
	GRPC          *GRPCEvent     `json:"grpc,omitempty"`
	Protobuf      *ProtobufEvent `json:"protobuf,omitempty"`
	Raw           *RawEvent      `json:"raw,omitempty"`
}

type HTTPEvent struct {
	Version         string      `json:"version"`
	Method          string      `json:"method,omitempty"`
	Host            string      `json:"host,omitempty"`
	Path            string      `json:"path,omitempty"`
	Status          int         `json:"status,omitempty"`
	RequestHeaders  http.Header `json:"request_headers,omitempty"`
	ResponseHeaders http.Header `json:"response_headers,omitempty"`
}

type GRPCEvent struct {
	Service string `json:"service,omitempty"`
	Method  string `json:"method,omitempty"`
	Path    string `json:"path,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ProtobufEvent is the decoded view of one or more gRPC messages. Raw always
// remains available on the same event so decoding can never hide source bytes.
type ProtobufEvent struct {
	Format      string `json:"format"`
	Schema      string `json:"schema"`
	MessageType string `json:"message_type,omitempty"`
	Data        string `json:"data,omitempty"`
	Error       string `json:"error,omitempty"`
}

// RawEvent contains binary-safe, unredacted application data. HTTP is a
// canonical dump after net/http parsing; gRPC is the original framed body.
type RawEvent struct {
	Format    string `json:"format"`
	Direction string `json:"direction"`
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
}

type CapturePolicy struct {
	CaptureBodies bool
	MaxBodyBytes  int64
}

type requestTrace struct {
	flowID      string
	started     time.Time
	protocol    Protocol
	destination string
}

type requestTraceKey struct{}

func classifyProtocol(request *http.Request) Protocol {
	isTLS := request.TLS != nil || strings.EqualFold(request.URL.Scheme, "https")
	isGRPC := request.ProtoMajor == 2 && strings.HasPrefix(
		strings.ToLower(request.Header.Get("Content-Type")),
		"application/grpc",
	)
	if isGRPC && isTLS {
		return ProtocolGRPCS
	}
	if isGRPC {
		return ProtocolGRPC
	}
	if isTLS {
		return ProtocolHTTPS
	}
	return ProtocolHTTP
}

func isTLSProtocol(protocol Protocol) bool {
	return protocol == ProtocolHTTPS || protocol == ProtocolGRPCS
}

func splitGRPCPath(path string) (string, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func newEventID() string {
	return uuid.NewString()
}
