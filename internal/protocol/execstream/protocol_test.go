package execstream

import (
	"bytes"
	"testing"
)

func TestFramesRoundTripWithoutMixingChannels(t *testing.T) {
	for _, frame := range []Frame{
		{Type: Stdin, Payload: []byte("input")},
		{Type: Stdout, Payload: []byte("output")},
		{Type: Stderr, Payload: []byte("error")},
		{Type: CloseStdin},
	} {
		encoded, err := Encode(frame)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(encoded)
		if err != nil || decoded.Type != frame.Type || !bytes.Equal(decoded.Payload, frame.Payload) {
			t.Fatalf("frame = %#v decoded = %#v err = %v", frame, decoded, err)
		}
	}
}

func TestResizeAndExitRoundTrip(t *testing.T) {
	resize, err := EncodeResize(TerminalSize{Width: 120, Height: 40})
	if err != nil {
		t.Fatal(err)
	}
	resizeFrame, _ := Decode(resize)
	if size, err := DecodeResize(resizeFrame); err != nil || size.Width != 120 || size.Height != 40 {
		t.Fatalf("size = %#v err = %v", size, err)
	}
	exit, err := EncodeExit(ExitStatus{Code: 137, Cancelled: true, Error: "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	exitFrame, _ := Decode(exit)
	if status, err := DecodeExit(
		exitFrame,
	); err != nil || status.Code != 137 || !status.Cancelled ||
		status.Error != "cancelled" {
		t.Fatalf("status = %#v err = %v", status, err)
	}
}
