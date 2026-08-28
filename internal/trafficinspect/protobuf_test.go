package trafficinspect

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/fengqi-dev/kube-loop/internal/utils"
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

func TestDecodeWireMessageCoversWireTypes(t *testing.T) {
	payload := protowire.AppendTag(nil, 1, protowire.VarintType)
	payload = protowire.AppendVarint(payload, ^uint64(0))
	payload = protowire.AppendTag(payload, 2, protowire.Fixed32Type)
	payload = protowire.AppendFixed32(payload, ^uint32(0))
	payload = protowire.AppendTag(payload, 3, protowire.Fixed64Type)
	payload = protowire.AppendFixed64(payload, ^uint64(0))
	payload = protowire.AppendTag(payload, 4, protowire.BytesType)
	payload = protowire.AppendString(payload, "hello")
	nested := protowire.AppendTag(nil, 1, protowire.VarintType)
	nested = protowire.AppendVarint(nested, 7)
	payload = protowire.AppendTag(payload, 5, protowire.BytesType)
	payload = protowire.AppendBytes(payload, nested)
	payload = protowire.AppendTag(payload, 6, protowire.BytesType)
	payload = protowire.AppendBytes(payload, []byte{0xff})
	payload = protowire.AppendTag(payload, 7, protowire.StartGroupType)
	payload = protowire.AppendTag(payload, 1, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 1)
	payload = protowire.AppendTag(payload, 7, protowire.EndGroupType)

	total := 0
	fields, consumed, err := decodeWireMessage(payload, 0, &total)
	if err != nil || consumed != len(payload) {
		t.Fatalf("decodeWireMessage() consumed=%d err=%v", consumed, err)
	}
	if fields["1"].([]wireValue)[0].Signed != "-1" ||
		fields["2"].([]wireValue)[0].Signed != "-1" ||
		fields["3"].([]wireValue)[0].Signed != "-1" {
		t.Fatalf("signed wire values=%#v", fields)
	}
	if fields["4"].([]wireValue)[0].Text != "hello" ||
		fields["5"].([]wireValue)[0].Message == nil ||
		fields["6"].([]wireValue)[0].Hex != "ff" ||
		fields["7"].([]wireValue)[0].WireType != "group" {
		t.Fatalf("decoded wire values=%#v", fields)
	}
}

func TestConsumeWireValueRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name     string
		wireType protowire.Type
		payload  []byte
	}{
		{name: "varint", wireType: protowire.VarintType, payload: []byte{0x80}},
		{name: "fixed32", wireType: protowire.Fixed32Type, payload: []byte{1}},
		{name: "fixed64", wireType: protowire.Fixed64Type, payload: []byte{1}},
		{name: "bytes", wireType: protowire.BytesType, payload: []byte{2, 1}},
		{name: "group", wireType: protowire.StartGroupType, payload: []byte{0x08}},
		{name: "end group", wireType: protowire.EndGroupType},
		{name: "unsupported", wireType: protowire.Type(7)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			total := 0
			if _, _, err := consumeWireValue(test.payload, 1, test.wireType, 0, &total); err == nil {
				t.Fatal("malformed wire value was accepted")
			}
		})
	}
}

func TestSignedFixed32(t *testing.T) {
	if got := signedFixed32(^uint32(0)); got != "-1" {
		t.Fatalf("signedFixed32(max) = %q, want -1", got)
	}
	if got := signedFixed32(7); got != "7" {
		t.Fatalf("signedFixed32(7) = %q, want 7", got)
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

func TestProtobufSchemaStoreImportsNestedDirectoryAndReloads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "example"), 0o700); err != nil {
		t.Fatal(err)
	}
	common := `syntax = "proto3"; package example.v1; message EchoMessage { string text = 1; }`
	service := `syntax = "proto3"; package example.v1; import "example/common.proto";
service EchoService { rpc Echo(EchoMessage) returns (EchoMessage); }`
	if err := os.WriteFile(filepath.Join(root, "example", "common.proto"), []byte(common), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.proto"), []byte(service), 0o600); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(t.TempDir(), "schemas.json")
	store, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err != nil {
		t.Fatalf("import proto directory: %v", err)
	}
	want := []string{"example/common.proto", "service.proto"}
	if got := store.Files(); !reflect.DeepEqual(got, want) {
		t.Fatalf("schema files = %v, want %v", got, want)
	}

	reloaded, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatalf("reload proto schemas: %v", err)
	}
	if got := reloaded.Files(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded schema files = %v, want %v", got, want)
	}
}

func TestProtobufSchemaStoreRejectsInvalidReplacementWithoutChangingActiveSet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewProtobufSchemaStore(filepath.Join(t.TempDir(), "schemas.json"), NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "broken.proto"),
		[]byte(`syntax = "proto3"; this is invalid`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err == nil {
		t.Fatal("invalid protobuf replacement succeeded")
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"valid.proto"}) {
		t.Fatalf("active schemas changed after failed import: %v", got)
	}
}

func TestProtobufSchemaStoreActivatesPersistedCompileAfterContextCancellation(t *testing.T) {
	initial := t.TempDir()
	if err := os.WriteFile(filepath.Join(initial, "initial.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(t.TempDir(), "schemas.json")
	store, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	replacement := t.TempDir()
	if err := os.WriteFile(filepath.Join(replacement, "replacement.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		err := utils.WriteFile(path, raw, dirMode, fileMode)
		cancel()
		return err
	}
	if err := store.ReplaceDirectory(ctx, replacement); err != nil {
		t.Fatalf("replace after durable write: %v", err)
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"replacement.proto"}) {
		t.Fatalf("active schemas after durable write = %v", got)
	}
	reloaded, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Files(); !reflect.DeepEqual(got, []string{"replacement.proto"}) {
		t.Fatalf("persisted schemas after durable write = %v", got)
	}
}

func TestProtobufSchemaStoreSerializesConcurrentReplacements(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if err := os.WriteFile(filepath.Join(first, "first.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "second.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewProtobufSchemaStore(filepath.Join(t.TempDir(), "schemas.json"), NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return utils.WriteFile(path, raw, dirMode, fileMode)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- store.ReplaceDirectory(context.Background(), first) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first protobuf replacement did not start persistence")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- store.ReplaceDirectory(context.Background(), second) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second replacement bypassed the first transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"second.proto"}) {
		t.Fatalf("final schemas = %v", got)
	}
}
