package trafficinspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	pathpkg "path"
	"slices"
	"strings"
	"sync"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
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
		// This mirrors mitmproxy's schema-less fallback behavior.
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
