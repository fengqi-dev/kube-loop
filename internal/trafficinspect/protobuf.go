package trafficinspect

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const protobufSchemaWire = "wire"

const (
	protobufMaxDepth       = 32
	protobufMaxFields      = 10_000
	protobufMaxDecodedSize = 16 << 20
)

// ProtobufDecoder dynamically resolves uploaded .proto sources without code
// generation. ReplaceSources atomically swaps the active descriptors only
// after every source has compiled successfully.
type ProtobufDecoder struct {
	mu      sync.RWMutex
	methods map[string]protobufMethod
}

type protobufMethod struct {
	input  protoreflect.MessageDescriptor
	output protoreflect.MessageDescriptor
}

func NewProtobufDecoder() *ProtobufDecoder {
	return &ProtobufDecoder{methods: make(map[string]protobufMethod)}
}

// ReplaceSources compiles an import-path-to-source map using a pure-Go parser.
// Keys must use the same relative paths referenced by import statements.
func (d *ProtobufDecoder) ReplaceSources(ctx context.Context, sources map[string]string) error {
	if d == nil {
		return errors.New("protobuf decoder is unavailable")
	}
	if len(sources) == 0 {
		d.mu.Lock()
		d.methods = make(map[string]protobufMethod)
		d.mu.Unlock()
		return nil
	}
	sources = cloneStringMap(sources)
	paths := make([]string, 0, len(sources))
	for path := range sources {
		cleaned := strings.TrimSpace(path)
		invalidPath := cleaned == "" || cleaned != path || strings.Contains(path, "\\") ||
			pathpkg.IsAbs(path) || pathpkg.Clean(path) != path || strings.HasPrefix(path, "../") ||
			!strings.HasSuffix(strings.ToLower(path), ".proto")
		if invalidPath {
			return fmt.Errorf("invalid protobuf source path %q", path)
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(
		&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(sources)},
	)}
	files, err := compiler.Compile(ctx, paths...)
	if err != nil {
		return fmt.Errorf("compile protobuf sources: %w", err)
	}
	methods := make(map[string]protobufMethod)
	for _, file := range files {
		services := file.Services()
		for serviceIndex := range services.Len() {
			service := services.Get(serviceIndex)
			serviceMethods := service.Methods()
			for methodIndex := range serviceMethods.Len() {
				method := serviceMethods.Get(methodIndex)
				path := "/" + string(service.FullName()) + "/" + string(method.Name())
				methods[path] = protobufMethod{input: method.Input(), output: method.Output()}
			}
		}
	}
	d.mu.Lock()
	d.methods = methods
	d.mu.Unlock()
	return nil
}

func (d *ProtobufDecoder) replaceCompiled(compiled *ProtobufDecoder) {
	compiled.mu.RLock()
	methods := maps.Clone(compiled.methods)
	compiled.mu.RUnlock()
	d.mu.Lock()
	d.methods = methods
	d.mu.Unlock()
}

func (d *ProtobufDecoder) Decode(path, direction, encoding string, framed []byte) *ProtobufEvent {
	if d == nil {
		return nil
	}
	descriptor := d.resolve(path, direction)
	messages, err := decodeGRPCFrames(framed, encoding, descriptor)
	if err != nil && descriptor != nil {
		// A stale or mismatched schema must not make captured traffic opaque.
		// Fall back to schema-less payload inspection when no descriptor matches.
		descriptor = nil
		messages, err = decodeGRPCFrames(framed, encoding, nil)
	}
	event := &ProtobufEvent{Format: "json", Schema: protobufSchemaWire}
	if descriptor != nil {
		event.Schema = "proto"
		event.MessageType = string(descriptor.FullName())
	}
	if err != nil {
		event.Error = err.Error()
		return event
	}
	raw, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		event.Error = "encode decoded protobuf"
		return event
	}
	event.Data = string(raw)
	return event
}

func (d *ProtobufDecoder) resolve(path, direction string) protoreflect.MessageDescriptor {
	d.mu.RLock()
	method, ok := d.methods[path]
	d.mu.RUnlock()
	if !ok {
		return nil
	}
	if direction == directionResponse {
		return method.output
	}
	return method.input
}

func decodeProtobufMessage(payload []byte, descriptor protoreflect.MessageDescriptor) (json.RawMessage, error) {
	if descriptor != nil {
		message := dynamicpb.NewMessage(descriptor)
		if err := proto.Unmarshal(payload, message); err != nil {
			return nil, fmt.Errorf("unmarshal %s: %w", descriptor.FullName(), err)
		}
		raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("marshal %s as JSON: %w", descriptor.FullName(), err)
		}
		unknown := message.GetUnknown()
		if len(unknown) == 0 {
			return raw, nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("decode %s JSON: %w", descriptor.FullName(), err)
		}
		unknownFields, consumed, err := decodeWireMessage(unknown, 0, new(int))
		if err != nil || consumed != len(unknown) {
			decoded["_unknown_fields_hex"] = hex.EncodeToString(unknown)
		} else {
			decoded["_unknown_fields"] = unknownFields
		}
		raw, err = json.Marshal(decoded)
		if err != nil {
			return nil, fmt.Errorf("marshal %s unknown fields: %w", descriptor.FullName(), err)
		}
		return raw, nil
	}
	fields, consumed, err := decodeWireMessage(payload, 0, new(int))
	if err != nil {
		return nil, err
	}
	if consumed != len(payload) {
		return nil, errors.New("protobuf payload contains trailing bytes")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, errors.New("marshal inferred protobuf as JSON")
	}
	return raw, nil
}

func decodeGRPCFrames(
	framed []byte,
	encoding string,
	descriptor protoreflect.MessageDescriptor,
) ([]json.RawMessage, error) {
	messages := make([]json.RawMessage, 0, 1)
	for frameIndex := 0; len(framed) > 0; frameIndex++ {
		if len(framed) < 5 {
			return nil, fmt.Errorf("decode gRPC frame %d: header is truncated", frameIndex)
		}
		compressed := framed[0]
		if compressed > 1 {
			return nil, fmt.Errorf("decode gRPC frame %d: invalid compressed flag %d", frameIndex, compressed)
		}
		length := int(uint32(framed[1])<<24 | uint32(framed[2])<<16 | uint32(framed[3])<<8 | uint32(framed[4]))
		framed = framed[5:]
		if length > len(framed) {
			return nil, fmt.Errorf(
				"decode gRPC frame %d: payload length %d exceeds %d bytes",
				frameIndex,
				length,
				len(framed),
			)
		}
		payload := framed[:length]
		framed = framed[length:]
		if compressed == 1 {
			var err error
			payload, err = decompressGRPC(payload, encoding)
			if err != nil {
				return nil, fmt.Errorf("decode gRPC frame %d: %w", frameIndex, err)
			}
		}
		decoded, err := decodeProtobufMessage(payload, descriptor)
		if err != nil {
			return nil, fmt.Errorf("decode gRPC frame %d: %w", frameIndex, err)
		}
		messages = append(messages, decoded)
	}
	return messages, nil
}

func decompressGRPC(payload []byte, encoding string) ([]byte, error) {
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	if encoding == "" || encoding == "identity" {
		return nil, errors.New("compressed gRPC frame has no compression encoding")
	}
	var (
		reader io.ReadCloser
		err    error
	)
	switch encoding {
	case "gzip":
		reader, err = gzip.NewReader(bytes.NewReader(payload))
	case "deflate":
		reader, err = zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			reader = flate.NewReader(bytes.NewReader(payload))
			err = nil
		}
	default:
		return nil, fmt.Errorf("unsupported gRPC compression %q", encoding)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s stream: %w", encoding, err)
	}
	defer func() { _ = reader.Close() }()
	decoded, err := io.ReadAll(io.LimitReader(reader, protobufMaxDecodedSize+1))
	if err != nil {
		return nil, fmt.Errorf("decompress %s payload: %w", encoding, err)
	}
	if len(decoded) > protobufMaxDecodedSize {
		return nil, errors.New("decompressed protobuf exceeds 16 MiB")
	}
	return decoded, nil
}

type wireValue struct {
	WireType string         `json:"wire_type"`
	Value    string         `json:"value,omitempty"`
	Signed   string         `json:"signed,omitempty"`
	ZigZag   string         `json:"zigzag,omitempty"`
	Text     string         `json:"text,omitempty"`
	Hex      string         `json:"hex,omitempty"`
	Message  map[string]any `json:"message,omitempty"`
}

func decodeWireMessage(payload []byte, depth int, total *int) (map[string]any, int, error) {
	if depth > protobufMaxDepth {
		return nil, 0, errors.New("protobuf nesting exceeds 32 levels")
	}
	result := make(map[string]any)
	originalLength := len(payload)
	for len(payload) > 0 {
		*total++
		if *total > protobufMaxFields {
			return nil, 0, errors.New("protobuf field count exceeds 10000")
		}
		number, wireType, consumed := protowire.ConsumeTag(payload)
		if consumed < 0 || number < 1 {
			return nil, 0, errors.New("invalid protobuf field tag")
		}
		payload = payload[consumed:]
		value, valueConsumed, err := consumeWireValue(payload, number, wireType, depth, total)
		if err != nil {
			return nil, 0, fmt.Errorf("field %d: %w", number, err)
		}
		payload = payload[valueConsumed:]
		key := strconv.Itoa(int(number))
		values, _ := result[key].([]wireValue)
		result[key] = append(values, value)
	}
	return result, originalLength - len(payload), nil
}

func consumeWireValue(
	payload []byte,
	number protowire.Number,
	wireType protowire.Type,
	depth int,
	total *int,
) (wireValue, int, error) {
	switch wireType {
	case protowire.VarintType:
		value, consumed := protowire.ConsumeVarint(payload)
		if consumed < 0 {
			return wireValue{}, 0, errors.New("invalid varint")
		}
		return wireValue{
			WireType: "varint",
			Value:    strconv.FormatUint(value, 10),
			Signed:   signedVarint(value),
			ZigZag:   strconv.FormatInt(protowire.DecodeZigZag(value), 10),
		}, consumed, nil
	case protowire.Fixed32Type:
		value, consumed := protowire.ConsumeFixed32(payload)
		if consumed < 0 {
			return wireValue{}, 0, errors.New("invalid fixed32")
		}
		return wireValue{
			WireType: "fixed32",
			Value:    strconv.FormatUint(uint64(value), 10),
			Signed:   signedFixed32(value),
		}, consumed, nil
	case protowire.Fixed64Type:
		value, consumed := protowire.ConsumeFixed64(payload)
		if consumed < 0 {
			return wireValue{}, 0, errors.New("invalid fixed64")
		}
		return wireValue{
			WireType: "fixed64",
			Value:    strconv.FormatUint(value, 10),
			Signed:   signedVarint(value),
		}, consumed, nil
	case protowire.BytesType:
		value, consumed := protowire.ConsumeBytes(payload)
		if consumed < 0 {
			return wireValue{}, 0, errors.New("invalid length-delimited value")
		}
		decoded := wireValue{WireType: "bytes"}
		if utf8.Valid(value) && isPrintableText(string(value)) {
			decoded.Text = string(value)
		} else if len(value) > 0 {
			childTotal := *total
			nested, nestedConsumed, nestedErr := decodeWireMessage(value, depth+1, &childTotal)
			if nestedErr == nil && nestedConsumed == len(value) {
				decoded.Message = nested
				*total = childTotal
			} else {
				decoded.Hex = hex.EncodeToString(value)
			}
		}
		return decoded, consumed, nil
	case protowire.StartGroupType:
		value, consumed := protowire.ConsumeGroup(number, payload)
		if consumed < 0 {
			return wireValue{}, 0, errors.New("invalid group")
		}
		return wireValue{WireType: "group", Hex: hex.EncodeToString(value)}, consumed, nil
	case protowire.EndGroupType:
		return wireValue{}, 0, errors.New("unexpected end group")
	default:
		return wireValue{}, 0, fmt.Errorf("unsupported wire type %d", wireType)
	}
}

func isPrintableText(value string) bool {
	for _, character := range value {
		if character < ' ' && character != '\n' && character != '\r' && character != '\t' {
			return false
		}
	}
	return value != ""
}

func signedVarint(value uint64) string {
	return strconv.FormatInt(int64(value), 10) //nolint:gosec // Intentional two's-complement reinterpretation.
}

func signedFixed32(value uint32) string {
	return strconv.FormatInt(int64(int32(value)), 10) //nolint:gosec // Intentional 32-bit reinterpretation.
}

const (
	protobufStoreVersion   = 1
	protobufMaxSourceSize  = 2 << 20
	protobufMaxSourceAll   = 16 << 20
	protobufMaxSourceFiles = 1_000
)

type persistedProtobufSources struct {
	Version int               `json:"version"`
	Sources map[string]string `json:"sources"`
}

// ProtobufSchemaStore persists the source set as one profile-scoped document.
// Keeping import paths as map keys lets protocompile resolve nested imports.
type ProtobufSchemaStore struct {
	replaceMu sync.Mutex
	mu        sync.RWMutex
	path      string
	decoder   *ProtobufDecoder
	sources   map[string]string
	writeFile func(string, []byte, os.FileMode, os.FileMode) error
}

func NewProtobufSchemaStore(path string, decoder *ProtobufDecoder) (*ProtobufSchemaStore, error) {
	if decoder == nil {
		return nil, errors.New("protobuf decoder is unavailable")
	}
	if strings.TrimSpace(path) == "" {
		layout, err := utils.Default()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(layout.DataDir(), "traffic-inspection", "protobuf.json")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve protobuf schema path: %w", err)
	}
	return &ProtobufSchemaStore{
		path: absolute, decoder: decoder, sources: make(map[string]string), writeFile: utils.WriteFile,
	}, nil
}

func (s *ProtobufSchemaStore) Load(ctx context.Context) error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read protobuf schemas: %w", err)
	}
	if len(raw) > protobufMaxSourceAll+(1<<20) {
		return errors.New("protobuf schema store exceeds 17 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var persisted persistedProtobufSources
	if err := decoder.Decode(&persisted); err != nil {
		return fmt.Errorf("decode protobuf schema store: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("protobuf schema store must contain one JSON document")
	}
	if persisted.Version != protobufStoreVersion {
		return fmt.Errorf("unsupported protobuf schema store version %d", persisted.Version)
	}
	return s.replace(ctx, persisted.Sources, false)
}

func (s *ProtobufSchemaStore) ReplaceDirectory(ctx context.Context, root string) error {
	sources, err := readProtoDirectory(root)
	if err != nil {
		return err
	}
	return s.replace(ctx, sources, true)
}

func (s *ProtobufSchemaStore) Files() []string {
	s.mu.RLock()
	files := make([]string, 0, len(s.sources))
	for path := range s.sources {
		files = append(files, path)
	}
	s.mu.RUnlock()
	slices.Sort(files)
	return files
}

func (s *ProtobufSchemaStore) replace(ctx context.Context, sources map[string]string, persist bool) error {
	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()
	validated := NewProtobufDecoder()
	if err := validated.ReplaceSources(ctx, sources); err != nil {
		return err
	}
	if persist {
		raw, err := json.MarshalIndent(
			persistedProtobufSources{Version: protobufStoreVersion, Sources: sources},
			"",
			"  ",
		)
		if err != nil {
			return errors.New("encode protobuf schemas")
		}
		raw = append(raw, '\n')
		writeFile := s.writeFile
		if writeFile == nil {
			writeFile = utils.WriteFile
		}
		if err := writeFile(s.path, raw, 0o700, 0o600); err != nil {
			return fmt.Errorf("save protobuf schemas: %w", err)
		}
	}
	s.decoder.replaceCompiled(validated)
	s.mu.Lock()
	s.sources = cloneStringMap(sources)
	s.mu.Unlock()
	return nil
}

func readProtoDirectory(root string) (map[string]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("protobuf source directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve protobuf source directory: %w", err)
	}
	directory, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open protobuf source directory: %w", err)
	}
	defer func() { _ = directory.Close() }()
	sources := make(map[string]string)
	total := 0
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".proto") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(sources) >= protobufMaxSourceFiles {
			return errors.New("protobuf source directory exceeds 1000 files")
		}
		if info.Size() > protobufMaxSourceSize {
			return fmt.Errorf("protobuf source %s exceeds 2 MiB", entry.Name())
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return fmt.Errorf("resolve protobuf import path: %w", err)
		}
		raw, err := directory.ReadFile(relative)
		if err != nil {
			return fmt.Errorf("read protobuf source %s: %w", entry.Name(), err)
		}
		total += len(raw)
		if total > protobufMaxSourceAll {
			return errors.New("protobuf sources exceed 16 MiB")
		}
		sources[filepath.ToSlash(relative)] = string(raw)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan protobuf source directory: %w", err)
	}
	if len(sources) == 0 {
		return nil, errors.New("protobuf source directory contains no .proto files")
	}
	return sources, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	maps.Copy(cloned, source)
	return cloned
}
