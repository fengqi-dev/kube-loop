package distribution

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestVerifySHA256(t *testing.T) {
	t.Parallel()

	content := []byte("verified")
	if err := verifySHA256(content, contentSHA256(content)); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256(content, strings.Repeat("0", 64)); err == nil {
		t.Fatal("verifySHA256 accepted a mismatched digest")
	}
}

func TestExtractReleaseFilesRejectsUnsupportedArchive(t *testing.T) {
	t.Parallel()

	if _, err := extractReleaseFiles("sing-box.bin", []byte("binary")); err == nil ||
		!strings.Contains(err.Error(), "unsupported sing-box archive") {
		t.Fatalf("extractReleaseFiles() error = %v", err)
	}
}

func releaseArchive(t *testing.T, name string, files map[string][]byte) []byte {
	t.Helper()
	if strings.HasSuffix(name, ".zip") {
		return zipArchive(t, files)
	}
	return tarGzipArchive(t, files)
}

func zipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func tarGzipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func contentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
