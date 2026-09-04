package utils

import (
	"crypto/rand"
	"encoding/hex"
)

// randomHexBytes is the entropy behind RandomHexToken, sized so the hex form
// carries 256 bits.
const randomHexBytes = 32

// RandomHexToken returns a fresh 256-bit secret encoded as lowercase hex,
// suitable for bearer tokens and local control-socket credentials.
func RandomHexToken() (string, error) {
	value := make([]byte, randomHexBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
