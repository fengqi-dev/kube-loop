package wssprotocol

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHandshakeDocumentsRoundTripStrictly(t *testing.T) {
	limits := Limits{
		MaximumFrameBytes: 1 << 20, MaximumStreamFrameBytes: 64 << 10, MaximumStreamsPerConnection: 128,
		MaximumPhysicalConnections: 256, MaximumConnectionsPerUser: 8,
		StreamIdleTimeoutMillis: (30 * time.Minute).Milliseconds(),
	}
	for _, document := range []any{
		NewClientHello("2.4.0", "22222222-2222-4222-8222-222222222222"),
		NewServerHello("2.4.0", limits),
		NewReject(CodeVersionMismatch, "No compatible WSS protocol version", Version),
	} {
		raw, err := Encode(document)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.ClientHello == nil && decoded.ServerHello == nil && decoded.Reject == nil {
			t.Fatalf("decoded = %#v", decoded)
		}
	}
}

func TestHandshakeAdvertisesTunnelTrafficStreams(t *testing.T) {
	for name, capabilities := range map[string][]string{
		"client": NewClientHello("2.4.0", "22222222-2222-4222-8222-222222222222").Capabilities,
		"server": NewServerHello("2.4.0", Limits{}).Capabilities,
	} {
		t.Run(name, func(t *testing.T) {
			for _, capability := range capabilities {
				if capability == CapabilityTrafficWebSocket {
					return
				}
			}
			t.Fatal("traffic.websocket.v1 capability is missing")
		})
	}
}

func TestHandshakeRejectsUnknownMissingDuplicateAndOversizedValues(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"type":"client_hello","protocolVersions":["2.0"],"clientVersion":"2.0.0","deviceId":"device","capabilities":["smux.v2"],"unknown":true}`),
		[]byte(`{"type":"client_hello","protocolVersions":["2.0"],"clientVersion":"2.0.0","capabilities":["smux.v2"]}`),
		[]byte(`{"type":"client_hello","protocolVersions":["2.0","2.0"],"clientVersion":"2.0.0","deviceId":"device","capabilities":["smux.v2"]}`),
		[]byte(`{"type":"unknown"}`),
		bytes.Repeat([]byte{'x'}, MaximumHandshakeBytes+1),
	}
	for index, raw := range tests {
		if _, err := Decode(raw); err == nil {
			t.Fatalf("case %d accepted", index)
		}
	}
}

func TestVersionNegotiationAndClientMinimum(t *testing.T) {
	selected, err := Negotiate([]string{"2.1", "2.0"}, []string{"2.0"})
	if err != nil || selected != "2.0" {
		t.Fatalf("selected = %q, %v", selected, err)
	}
	if _, err := Negotiate([]string{"2.1"}, []string{"2.0"}); err == nil || !strings.Contains(err.Error(), CodeVersionMismatch) {
		t.Fatalf("version mismatch = %v", err)
	}
	if err := CheckClientVersion("2.0.0", "2.1.0"); err == nil || !strings.Contains(err.Error(), CodeClientVersionUnsupported) {
		t.Fatalf("client version = %v", err)
	}
	if err := CheckClientVersion("dev", "99.0.0"); err != nil {
		t.Fatal(err)
	}
}
