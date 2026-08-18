package trafficinspect

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

const testProto = `syntax = "proto3";
package example.v1;
service EchoService {
  rpc Echo(EchoRequest) returns (EchoResponse);
}
message EchoRequest { string text = 1; int32 count = 2; }
message EchoResponse { string reply = 1; }
`

func TestProtobufDecoderUsesDynamicProtoDescriptors(t *testing.T) {
	decoder := NewProtobufDecoder()
	if err := decoder.ReplaceSources(context.Background(), map[string]string{"echo.proto": testProto}); err != nil {
		t.Fatalf("compile test proto: %v", err)
	}
	payload := protowire.AppendTag(nil, 1, protowire.BytesType)
	payload = protowire.AppendString(payload, "hello")
	payload = protowire.AppendTag(payload, 2, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 7)

	event := decoder.Decode("/example.v1.EchoService/Echo", directionRequest, "", grpcFrame(false, payload))
	if event.Error != "" || event.Schema != "proto" || event.MessageType != "example.v1.EchoRequest" {
		t.Fatalf("decoded event = %#v", event)
	}
	if !strings.Contains(event.Data, `"text": "hello"`) || !strings.Contains(event.Data, `"count": 7`) {
		t.Fatalf("decoded proto JSON = %s", event.Data)
	}
}

func TestProtobufDecoderFallsBackToWireInference(t *testing.T) {
	decoder := NewProtobufDecoder()
	payload := protowire.AppendTag(nil, 1, protowire.BytesType)
	payload = protowire.AppendString(payload, "hello")
	payload = protowire.AppendTag(payload, 2, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 7)

	event := decoder.Decode("/unknown.Service/Call", directionRequest, "", grpcFrame(false, payload))
	if event.Error != "" || event.Schema != "wire" {
		t.Fatalf("decoded event = %#v", event)
	}
	var messages []map[string][]wireValue
	if err := json.Unmarshal([]byte(event.Data), &messages); err != nil {
		t.Fatalf("decode inferred JSON: %v", err)
	}
	if len(messages) != 1 || messages[0]["1"][0].Text != "hello" || messages[0]["2"][0].Value != "7" {
		t.Fatalf("inferred messages = %#v", messages)
	}
}

func TestProtobufDecoderHandlesGZIPAndStreamingFrames(t *testing.T) {
	decoder := NewProtobufDecoder()
	first := protowire.AppendTag(nil, 1, protowire.VarintType)
	first = protowire.AppendVarint(first, 1)
	second := protowire.AppendTag(nil, 1, protowire.VarintType)
	second = protowire.AppendVarint(second, 2)
	compressed := gzipBytes(t, first)
	framed := append(grpcFrame(true, compressed), grpcFrame(false, second)...)

	event := decoder.Decode("/unknown.Service/Stream", directionResponse, "gzip", framed)
	if event.Error != "" {
		t.Fatalf("decode streaming frames: %s", event.Error)
	}
	var messages []map[string][]wireValue
	if err := json.Unmarshal([]byte(event.Data), &messages); err != nil {
		t.Fatalf("decode streaming JSON: %v", err)
	}
	if len(messages) != 2 || messages[0]["1"][0].Value != "1" || messages[1]["1"][0].Value != "2" {
		t.Fatalf("streaming messages = %#v", messages)
	}
}

func TestProtobufDecoderReportsMalformedFrameWithoutChangingRawCapture(t *testing.T) {
	event := NewProtobufDecoder().Decode("/unknown.Service/Call", directionRequest, "", []byte{0, 0, 0})
	if !strings.Contains(event.Error, "header is truncated") || event.Data != "" {
		t.Fatalf("malformed frame result = %#v", event)
	}
}

func grpcFrame(compressed bool, payload []byte) []byte {
	frame := make([]byte, 5, len(payload)+5)
	if compressed {
		frame[0] = 1
	}
	binary.BigEndian.PutUint32(frame[1:], uint32(len(payload)))
	return append(frame, payload...)
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("compress payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buffer.Bytes()
}
