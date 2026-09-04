package exchangestream

import (
	"bytes"
	"strings"
	"testing"
)

// The frame layout itself is covered in internal/protocol/streamframe. What
// belongs here is the part this contract owns: that its own constants and
// Frame type travel through its own codec, and that it names itself when it
// refuses a frame.
func TestFramesRoundTripThroughContractTypes(t *testing.T) {
	want := Frame{Type: Data, StreamID: 1, Payload: []byte("payload")}
	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Type != want.Type || got.StreamID != want.StreamID || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("round trip got=%#v want=%#v", got, want)
	}
	open := Frame{Type: Open, StreamID: 1, ServicePort: 8080, Protocol: ProtocolTCP}
	if _, err := Encode(open); err != nil {
		t.Fatalf("encode open frame: %v", err)
	}
}

func TestRejectionsNameThisContract(t *testing.T) {
	if _, err := Encode(Frame{Type: Data, StreamID: 1, Payload: make([]byte, MaximumData+1)}); err == nil ||
		!strings.Contains(err.Error(), "exchange") {
		t.Fatalf("oversize rejection = %v, want one naming exchange", err)
	}
	if _, err := Decode([]byte{Ready}); err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Fatalf("truncated rejection = %v, want one naming exchange", err)
	}
}
