package trafficinspect

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

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
	// Protobuf signed views intentionally reinterpret the same two's-complement bits.
	return strconv.FormatInt(int64(value), 10) //nolint:gosec // Intentional two's-complement reinterpretation.
}

func signedFixed32(value uint32) string {
	// Protobuf fixed32 signed views intentionally reinterpret the same 32 bits.
	return strconv.FormatInt(int64(int32(value)), 10) //nolint:gosec // Intentional 32-bit reinterpretation.
}
