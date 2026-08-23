package distribution

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

func verifySHA256(content []byte, expected string) error {
	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("sing-box SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func extractReleaseFiles(name string, content []byte) (map[string][]byte, error) {
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return extractFromTarGz(content)
	case strings.HasSuffix(name, ".zip"):
		return extractFromZip(content)
	default:
		return nil, fmt.Errorf("unsupported sing-box archive %q", name)
	}
}

func extractFromZip(content []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open sing-box zip archive: %w", err)
	}
	files := make(map[string][]byte)
	for _, file := range reader.File {
		if !file.FileInfo().Mode().IsRegular() {
			continue
		}
		base := filepath.Base(file.Name)
		switch strings.ToLower(base) {
		case singBoxBinaryWin, wintunDLL, cronetDLL, "license":
		default:
			continue
		}
		opened, openErr := file.Open()
		if openErr != nil {
			return nil, fmt.Errorf("open %s: %w", base, openErr)
		}
		value, readErr := io.ReadAll(io.LimitReader(opened, 128<<20))
		if closeErr := opened.Close(); readErr != nil || closeErr != nil {
			return nil, fmt.Errorf("extract %s: %w", base, errors.Join(readErr, closeErr))
		}
		files[strings.ToLower(base)] = value
	}
	if _, ok := files[singBoxBinaryWin]; !ok {
		return nil, errors.New("sing-box zip archive does not contain an executable")
	}
	return files, nil
}

func extractFromTarGz(content []byte) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("open sing-box gzip archive: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read sing-box tar archive: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != singBoxBinary {
			continue
		}
		value, readErr := io.ReadAll(io.LimitReader(tarReader, 128<<20))
		if readErr != nil {
			return nil, fmt.Errorf("extract sing-box binary: %w", readErr)
		}
		files[singBoxBinary] = value
		return files, nil
	}
	return nil, errors.New("sing-box tar archive does not contain sing-box binary")
}
