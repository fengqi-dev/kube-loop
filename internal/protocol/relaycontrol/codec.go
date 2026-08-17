package relaycontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

type validMessage interface {
	Validate(time.Time) error
}

func DecodeRegistrationRequest(raw []byte, now time.Time) (RegistrationRequest, error) {
	return decode[RegistrationRequest](raw, now)
}

func DecodeRegistrationResponse(raw []byte, now time.Time) (RegistrationResponse, error) {
	return decode[RegistrationResponse](raw, now)
}

func DecodeHeartbeatRequest(raw []byte, now time.Time) (HeartbeatRequest, error) {
	return decode[HeartbeatRequest](raw, now)
}

func DecodeHeartbeatResponse(raw []byte, now time.Time) (HeartbeatResponse, error) {
	return decode[HeartbeatResponse](raw, now)
}

func DecodeAllocationRequest(raw []byte, now time.Time) (AllocationRequest, error) {
	return decode[AllocationRequest](raw, now)
}

func DecodeAllocationResponse(raw []byte, now time.Time) (AllocationResponse, error) {
	return decode[AllocationResponse](raw, now)
}

func Encode[T validMessage](message T, now time.Time) ([]byte, error) {
	if err := message.Validate(now); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, errors.New("encode relay control message")
	}
	if len(encoded) > MaximumBodyBytes {
		return nil, errors.New("relay control message exceeds 64 KiB")
	}
	return encoded, nil
}

func decode[T validMessage](raw []byte, now time.Time) (T, error) {
	var message T
	if len(raw) == 0 || len(raw) > MaximumBodyBytes {
		return message, errors.New("relay control message size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return message, errors.New("decode relay control message")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return message, errors.New("relay control message must contain one JSON document")
	}
	if err := message.Validate(now.UTC()); err != nil {
		return message, err
	}
	return message, nil
}
