//go:build darwin

package supervisor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func newBytesReader(raw []byte) *bytes.Reader { return bytes.NewReader(raw) }

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		_ = file.Close()
		return "", fmt.Errorf("hash %s: %w", path, copyErr)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close %s after hashing: %w", path, err)
	}
	return digest, nil
}
