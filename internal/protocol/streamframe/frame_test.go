package streamframe

import (
	"bytes"
	"strings"
	"testing"
)

var codec = Codec{Name: "test"}

func TestFramesRoundTrip(t *testing.T) {
	frames := []Frame{
		{Type: Ready},
		{Type: Open, StreamID: 1, ServicePort: 8080, Protocol: ProtocolTCP},
		{Type: Data, StreamID: 1, Payload: []byte("request")},
		{Type: CloseWrite, StreamID: 1},
		{Type: Close, StreamID: 1, Payload: []byte("closed")},
		{Type: Datagram, StreamID: 2, ServicePort: 5353, Protocol: ProtocolUDP, Payload: []byte("dns")},
		{Type: Stop, Payload: []byte("Session ended")},
	}
	for _, want := range frames {
		encoded, err := codec.Encode(want)
		if err != nil {
			t.Fatalf("encode %#v: %v", want, err)
		}
		got, err := codec.Decode(encoded)
		if err != nil {
			t.Fatalf("decode %#v: %v", want, err)
		}
		if got.Type != want.Type || got.StreamID != want.StreamID || got.ServicePort != want.ServicePort ||
			got.Protocol != want.Protocol || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("round trip got=%#v want=%#v", got, want)
		}
	}
}

func TestFramesRejectCrossDirectionMetadataAndOversize(t *testing.T) {
	invalid := []Frame{
		{Type: Ready, StreamID: 1},
		{Type: Open, StreamID: 1, ServicePort: 80, Protocol: ProtocolUDP},
		{Type: Data, StreamID: 1},
		{Type: Data, StreamID: 1, Payload: make([]byte, MaximumData+1)},
		{Type: CloseWrite},
		{Type: Close, StreamID: 1, Payload: make([]byte, MaximumError+1)},
		{Type: Datagram, StreamID: 1, ServicePort: 53, Protocol: ProtocolTCP, Payload: []byte("x")},
		{Type: Datagram, StreamID: 1, ServicePort: 53, Protocol: ProtocolUDP, Payload: make([]byte, MaximumDatagram+1)},
		{Type: Stop, Protocol: ProtocolTCP},
		{Type: 255},
	}
	for _, frame := range invalid {
		if _, err := codec.Encode(frame); err == nil {
			t.Fatalf("invalid frame accepted: %#v", frame)
		}
	}
	if _, err := codec.Decode([]byte{Ready}); err == nil {
		t.Fatal("truncated frame accepted")
	}
}

// Every rejection has to name the contract, so a caller reading the log can
// tell which of the contracts sharing this layout refused the frame.
func TestErrorsCarryTheContractName(t *testing.T) {
	named := Codec{Name: "widget"}
	_, truncated := named.Decode([]byte{Ready})
	_, unsupported := named.Encode(Frame{Type: 255})
	_, malformed := named.Encode(Frame{Type: Ready, StreamID: 1})
	for _, err := range []error{truncated, unsupported, malformed} {
		if err == nil {
			t.Fatal("expected a rejection")
		}
		if !strings.Contains(err.Error(), "widget") {
			t.Fatalf("error %q does not name the contract", err)
		}
	}
}
