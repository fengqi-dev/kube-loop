package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// VerifyFileSHA256 verifies that path has the expected hexadecimal SHA-256 digest.
func VerifyFileSHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, copyErr := io.Copy(hash, file); copyErr != nil {
		return errors.Join(copyErr, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(strings.TrimSpace(expected), actual) {
		return fmt.Errorf("SHA-256 mismatch")
	}
	return nil
}
