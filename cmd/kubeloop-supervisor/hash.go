//go:build darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		return "", errors.Join(copyErr, file.Close())
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if err := file.Close(); err != nil {
		return "", err
	}
	return digest, nil
}
