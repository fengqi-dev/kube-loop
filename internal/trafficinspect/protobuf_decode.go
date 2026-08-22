package trafficinspect

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

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
