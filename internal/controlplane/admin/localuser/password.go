package localuser

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

func hashPassword(password []byte, source io.Reader) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(source, salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey(password, salt, 3, 64*1024, 2, 32)
	defer clear(hash)
	return fmt.Sprintf(
		"$argon2id$v=19$m=65536,t=3,p=2$%s$%s",
		base64.RawStdEncoding.EncodeToString(
			salt,
		),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyPassword(password []byte, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 || memory != 64*1024 || iterations != 3 ||
		parallelism != 2 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey(
		password,
		salt,
		iterations,
		memory,
		parallelism,
		32,
	)
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func normalizeUsername(
	value string,
) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validUsername(value string) bool {
	if value == "" || len(value) > 128 ||
		strings.ContainsAny(value, "\x00\r\n\t ") {
		return false
	}
	for _, character := range value {
		letter := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		symbol := character == '.' || character == '_' || character == '-' ||
			character == '@'
		if !letter && !digit && !symbol {
			return false
		}
	}
	return true
}
