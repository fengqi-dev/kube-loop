package relayticket

import (
	"encoding/base64"
)

func decodePart(value string, maximum int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > maximum {
		return nil, ErrInvalid
	}
	return decoded, nil
}
