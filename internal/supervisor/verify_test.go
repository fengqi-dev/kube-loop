package supervisor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker")
	content := []byte("worker")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	if err := VerifyFileSHA256(path, digest); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFileSHA256(path, fmt.Sprintf("%064x", 0)); err == nil {
		t.Fatal("mismatched digest accepted")
	}
}
