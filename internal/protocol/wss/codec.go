package wss

import (
	"encoding/json"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func Encode(message any) ([]byte, error) {
	if err := validateMessage(message); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(message)
	if err != nil || len(raw) > MaximumHandshakeBytes {
		return nil, ErrInvalidHandshake
	}
	return raw, nil
}

func Decode(raw []byte) (Message, error) {
	if len(raw) == 0 || len(raw) > MaximumHandshakeBytes {
		return Message{}, ErrInvalidHandshake
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Message{}, ErrInvalidHandshake
	}
	switch envelope.Type {
	case KindClientHello:
		var hello ClientHello
		if err := utils.DecodeStrictJSON(raw, &hello); err != nil || hello.Validate() != nil {
			return Message{}, ErrInvalidHandshake
		}
		return Message{ClientHello: &hello}, nil
	case KindServerHello:
		var hello ServerHello
		if err := utils.DecodeStrictJSON(raw, &hello); err != nil || hello.Validate() != nil {
			return Message{}, ErrInvalidHandshake
		}
		return Message{ServerHello: &hello}, nil
	case KindReject:
		var reject Reject
		if err := utils.DecodeStrictJSON(raw, &reject); err != nil || reject.Validate() != nil {
			return Message{}, ErrInvalidHandshake
		}
		return Message{Reject: &reject}, nil
	default:
		return Message{}, ErrInvalidHandshake
	}
}

func validateMessage(message any) error {
	switch value := message.(type) {
	case ClientHello:
		return value.Validate()
	case *ClientHello:
		if value == nil {
			return ErrInvalidHandshake
		}
		return value.Validate()
	case ServerHello:
		return value.Validate()
	case *ServerHello:
		if value == nil {
			return ErrInvalidHandshake
		}
		return value.Validate()
	case Reject:
		return value.Validate()
	case *Reject:
		if value == nil {
			return ErrInvalidHandshake
		}
		return value.Validate()
	default:
		return ErrInvalidHandshake
	}
}
