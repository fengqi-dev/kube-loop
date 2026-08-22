package trafficinspect

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

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
