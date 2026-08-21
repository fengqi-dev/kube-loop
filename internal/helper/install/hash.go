package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

func fileSHA256(path string) (string, error) {
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
