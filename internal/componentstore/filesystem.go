package componentstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func directoryFor(release string) (string, error) {
	release, err := safeSegment("release", release)
	if err != nil {
		return "", err
	}
	layout, err := utils.ForVersion(release)
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.CacheDir(), "components", release, runtime.GOOS+"-"+runtime.GOARCH), nil
}

func safeSegment(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return "", fmt.Errorf("invalid %s %q", label, value)
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._-+", char) {
			continue
		}
		return "", fmt.Errorf("invalid %s %q", label, value)
	}
	return value, nil
}

func readManifest(directory string) (manifest, error) {
	file, err := os.Open(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return manifest{}, err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, fmt.Errorf("decode component manifest: %w", err)
	}
	if value.Version != manifestVersion {
		return manifest{}, fmt.Errorf("unsupported component manifest version %d", value.Version)
	}
	return value, nil
}

func writeManifest(directory string, value manifest) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode component manifest: %w", err)
	}
	content = append(content, '\n')
	temporary, err := os.CreateTemp(directory, ".manifest-*")
	if err != nil {
		return fmt.Errorf("create component manifest: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := activateFile(temporaryPath, filepath.Join(directory, "manifest.json")); err != nil {
		return fmt.Errorf("activate component manifest: %w", err)
	}
	return os.Chmod(filepath.Join(directory, "manifest.json"), 0o600)
}

func acquireReleaseLock(directory string) (func(), error) {
	lockPath := filepath.Join(directory, ".lock")
	for range 100 {
		if err := os.Mkdir(lockPath, 0o700); err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("lock component directory: %w", err)
		}

		if info, err := os.Stat(lockPath); err == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("lock component directory: timed out")
}

func activateFile(source, destination string) error {
	if runtime.GOOS == "windows" {
		if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(source, destination)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
