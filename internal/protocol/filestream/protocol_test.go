package filestream

import (
	"bytes"
	"strings"
	"testing"
)

func TestDataProgressAndControlFramesRoundTrip(t *testing.T) {
	data, err := Encode(Frame{Type: Data, Payload: []byte("payload")})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil || decoded.Type != Data || string(decoded.Payload) != "payload" {
		t.Fatalf("data frame = %#v err = %v", decoded, err)
	}
	progress, err := EncodeProgress(ProgressStatus{Transferred: 25, Total: 100})
	if err != nil {
		t.Fatal(err)
	}
	progressFrame, _ := Decode(progress)
	progressStatus, err := DecodeProgress(progressFrame)
	if err != nil || progressStatus.Transferred != 25 || progressStatus.Total != 100 {
		t.Fatalf("progress = %#v err = %v", progressStatus, err)
	}
	unknown, err := EncodeProgress(ProgressStatus{Transferred: 25})
	if err != nil {
		t.Fatal(err)
	}
	unknownFrame, _ := Decode(unknown)
	unknownStatus, err := DecodeProgress(unknownFrame)
	if err != nil || unknownStatus.Transferred != 25 || unknownStatus.Total != 0 {
		t.Fatalf("unknown-total progress = %#v err = %v", unknownStatus, err)
	}
	for _, frameType := range []byte{Complete, Cancel} {
		encoded, err := Encode(Frame{Type: frameType})
		if err != nil {
			t.Fatal(err)
		}
		frame, err := Decode(encoded)
		if err != nil || frame.Type != frameType {
			t.Fatalf("control frame = %#v err = %v", frame, err)
		}
	}
}

func TestTransferResultPreservesChecksumStatusAndBoundedError(t *testing.T) {
	checksum, err := ParseChecksum(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeResult(TransferResult{
		Status: ResultFailed, Transferred: 42, Checksum: checksum, HasChecksum: true,
		Error: strings.Repeat("x", MaximumError+50),
	})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	result, err := DecodeResult(frame)
	if err != nil || result.Status != ResultFailed || result.Transferred != 42 || !result.HasChecksum ||
		result.Checksum != checksum || len(result.Error) != MaximumError {
		t.Fatalf("result = %#v err = %v", result, err)
	}
	if FormatChecksum(result.Checksum) != strings.Repeat("ab", 32) {
		t.Fatalf("checksum = %q", FormatChecksum(result.Checksum))
	}
}

func TestProtocolRejectsOversizeAndMalformedFrames(t *testing.T) {
	tests := []Frame{
		{Type: Data},
		{Type: Data, Payload: bytes.Repeat([]byte{1}, MaximumData+1)},
		{Type: Complete, Payload: []byte{1}},
		{Type: Progress, Payload: make([]byte, 15)},
		{Type: Result, Payload: make([]byte, resultHeader-1)},
		{Type: 99},
	}
	for _, frame := range tests {
		if _, err := Encode(frame); err == nil {
			t.Fatalf("invalid frame was accepted: %#v", frame)
		}
	}
	if _, err := EncodeProgress(ProgressStatus{Transferred: 2, Total: 1}); err == nil {
		t.Fatal("invalid progress was accepted")
	}
	if _, err := ParseChecksum("not-a-checksum"); err == nil {
		t.Fatal("invalid checksum was accepted")
	}
	if _, err := EncodeResult(TransferResult{Status: ResultSucceeded, Error: "failure"}); err == nil {
		t.Fatal("successful result with error was accepted")
	}
}
