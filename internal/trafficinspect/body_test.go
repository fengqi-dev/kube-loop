package trafficinspect

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestObservedBodyCapturesWithoutChangingReads(t *testing.T) {
	var captured capturedBody
	body := observeBody(io.NopCloser(strings.NewReader("abcdefgh")), 5, func(result capturedBody) {
		captured = result
	})
	read, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(read) != "abcdefgh" {
		t.Fatalf("read body = %q", read)
	}
	if string(captured.data) != "abcde" || captured.size != 8 || !captured.truncated {
		t.Fatalf("captured body = %#v", captured)
	}
}

func TestRawHTTPMessageIsUnredacted(t *testing.T) {
	request := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "http", Host: "raw.test", Path: "/submit"},
		Host:   "raw.test",
		Proto:  "HTTP/1.1",
		Header: http.Header{
			"Authorization": []string{"Bearer unredacted-secret"},
			"Content-Type":  []string{"application/octet-stream"},
		},
		Body: io.NopCloser(bytes.NewReader(nil)),
	}
	raw := rawHTTPMessage(
		request,
		nil,
		directionRequest,
		"application/octet-stream",
		capturedBody{data: []byte{0, 1, 2}, size: 3},
	)
	decoded, err := base64.StdEncoding.DecodeString(raw.Data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(decoded, []byte("Authorization: Bearer unredacted-secret\r\n")) {
		t.Fatalf("raw HTTP headers = %q", decoded)
	}
	if !bytes.HasSuffix(decoded, []byte{0, 1, 2}) {
		t.Fatalf("raw HTTP body = %x", decoded)
	}
}

func TestEmitCapturedBodyKeepsRawGRPCFrames(t *testing.T) {
	events := make(chan Event, 1)
	request := &http.Request{
		Method:     http.MethodPost,
		URL:        &url.URL{Scheme: "https", Path: "/grpcbin.GRPCBin/DummyUnary"},
		Host:       "grpcbin:9001",
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		Header:     http.Header{"Content-Type": []string{"application/grpc"}},
	}
	frame := []byte{0, 0, 0, 0, 3, 1, 2, 3}
	emitCapturedBody(
		Config{OnEvent: func(event Event) { events <- event }},
		requestTrace{flowID: "flow", protocol: ProtocolGRPCS, destination: "grpcbin:9001"},
		request,
		nil,
		directionRequest,
		"application/grpc",
		capturedBody{data: frame, size: int64(len(frame))},
	)
	event := <-events
	if event.Raw == nil || event.Raw.Format != "grpc" || event.Raw.Data != base64.StdEncoding.EncodeToString(frame) {
		t.Fatalf("raw gRPC event = %#v", event)
	}
	if event.GRPC == nil || event.GRPC.Service != "grpcbin.GRPCBin" || event.GRPC.Method != "DummyUnary" {
		t.Fatalf("gRPC metadata = %#v", event.GRPC)
	}
	if event.Protobuf != nil {
		t.Fatalf("nil decoder unexpectedly emitted protobuf = %#v", event.Protobuf)
	}
}

func TestEmitCapturedBodyAddsProtobufAlongsideRawGRPC(t *testing.T) {
	events := make(chan Event, 1)
	request := &http.Request{
		Method:     http.MethodPost,
		URL:        &url.URL{Scheme: "http", Path: "/unknown.Service/Call"},
		Host:       "grpc.test",
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		Header:     http.Header{"Content-Type": []string{"application/grpc"}},
	}
	payload := []byte{0x08, 0x2a}
	frame := grpcFrame(false, payload)
	emitCapturedBody(
		Config{OnEvent: func(event Event) { events <- event }, Protobuf: NewProtobufDecoder()},
		requestTrace{flowID: "flow", protocol: ProtocolGRPC, destination: "grpc.test"},
		request,
		nil,
		directionRequest,
		"application/grpc",
		capturedBody{data: frame, size: int64(len(frame))},
	)
	event := <-events
	if event.Raw == nil || event.Protobuf == nil || event.Protobuf.Schema != "wire" {
		t.Fatalf("gRPC event = %#v", event)
	}
}

func TestWrapResponseBodyPreservesUpgradedConnection(t *testing.T) {
	body := &testReadWriteCloser{}
	response := &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		Body:       body,
	}
	wrapResponseBody(response, requestTrace{}, Config{Policy: CapturePolicy{CaptureBodies: true}})
	if response.Body != body {
		t.Fatalf("upgraded response body was wrapped as %T", response.Body)
	}
	if _, ok := response.Body.(io.Writer); !ok {
		t.Fatalf("upgraded response body no longer implements io.Writer: %T", response.Body)
	}
}

type testReadWriteCloser struct {
	bytes.Buffer
}

func (*testReadWriteCloser) Close() error { return nil }
