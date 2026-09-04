package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// FileSHA256 returns the lowercase hex SHA-256 digest of the file at path.
// Close failures are reported rather than swallowed, because callers use the
// digest to decide whether a binary may be executed and must not accept a
// digest taken from a file whose read did not complete cleanly.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		return "", fmt.Errorf("hash %s: %w", path, errors.Join(copyErr, file.Close()))
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close %s after hashing: %w", path, err)
	}
	return digest, nil
}
