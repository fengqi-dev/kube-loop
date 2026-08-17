package mirrorstream

import (
	"bytes"
	"testing"
)

func TestFramesRoundTrip(t *testing.T) {
	frames := []Frame{
		{Type: Ready},
		{Type: Open, StreamID: 1, ServicePort: 8080, Protocol: ProtocolTCP},
		{Type: Data, StreamID: 1, Payload: []byte("request")},
		{Type: CloseWrite, StreamID: 1},
		{Type: Close, StreamID: 1, Payload: []byte("shadow dropped")},
		{Type: Datagram, StreamID: 2, ServicePort: 5353, Protocol: ProtocolUDP, Payload: []byte("dns")},
		{Type: Stop, Payload: []byte("Session ended")},
	}
	for _, want := range frames {
		encoded, err := Encode(want)
		if err != nil {
			t.Fatalf("encode %#v: %v", want, err)
		}
		got, err := Decode(encoded)
		if err != nil {
			t.Fatalf("decode %#v: %v", want, err)
		}
		if got.Type != want.Type || got.StreamID != want.StreamID || got.ServicePort != want.ServicePort ||
			got.Protocol != want.Protocol || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("round trip got=%#v want=%#v", got, want)
		}
	}
}

func TestFramesRejectClientResponseMetadataAndOversize(t *testing.T) {
	invalid := []Frame{
		{Type: Ready, StreamID: 1},
		{Type: Open, StreamID: 1, ServicePort: 80, Protocol: ProtocolUDP},
		{Type: Data, StreamID: 1},
		{Type: Data, StreamID: 1, Payload: make([]byte, MaximumData+1)},
		{Type: CloseWrite},
		{Type: Close, StreamID: 1, Payload: make([]byte, MaximumError+1)},
		{Type: Datagram, StreamID: 1, ServicePort: 53, Protocol: ProtocolTCP, Payload: []byte("x")},
		{Type: Stop, Protocol: ProtocolTCP},
		{Type: 255},
	}
	for _, frame := range invalid {
		if _, err := Encode(frame); err == nil {
			t.Fatalf("invalid frame accepted: %#v", frame)
		}
	}
	if _, err := Decode([]byte{Ready}); err == nil {
		t.Fatal("truncated Mirror frame accepted")
	}
}
