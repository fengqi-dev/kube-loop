package execstream

import "testing"

func FuzzWebSocketExecFrameDecode(f *testing.F) {
	f.Add([]byte{Stdin, 'h', 'i'})
	f.Add([]byte{Resize, 0, 80, 0, 24})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > MaximumPayload+2 {
			t.Skip()
		}
		frame, err := Decode(encoded)
		if err != nil {
			return
		}
		roundTrip, err := Encode(frame)
		if err != nil {
			t.Fatalf("decoded frame could not be encoded: %v", err)
		}
		if string(roundTrip) != string(encoded) {
			t.Fatal("WebSocket exec frame round trip changed bytes")
		}
	})
}
