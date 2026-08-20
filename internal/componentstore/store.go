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
	"sync"
	"time"
)

const manifestVersion = 1

var storeMu sync.Mutex

type manifest struct {
	Version    int                      `json:"version"`
	Release    string                   `json:"release"`
	Platform   string                   `json:"platform"`
	Components map[string]manifestEntry `json:"components"`
}

type manifestEntry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Cache atomically stores a regular executable in the per-user component
// cache. The cache is only a distribution source; privileged callers must
// promote components into a protected system path before executing them.
func Cache(release, name, source string) (string, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	directory, err := directoryFor(release)
	if err != nil {
		return "", err
	}
	name, err = safeSegment("component name", name)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("stat component %s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("component %s source is not a regular file", name)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create component directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", fmt.Errorf("secure component directory: %w", err)
	}
	releaseUnlock, err := acquireReleaseLock(directory)
	if err != nil {
		return "", err
	}
	defer releaseUnlock()
	destination := filepath.Join(directory, name)
	if samePath(source, destination) {
		if _, err := findLocked(directory, name); err == nil {
			return destination, nil
		}
	}
	temporary, err := os.CreateTemp(directory, "."+name+"-*")
	if err != nil {
		return "", fmt.Errorf("create component staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o700); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("secure component staging file: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("open component %s: %w", name, err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hash), input)
	closeInputErr := input.Close()
	closeOutputErr := temporary.Close()
	if copyErr != nil {
		return "", fmt.Errorf("cache component %s: %w", name, copyErr)
	}
	if closeInputErr != nil {
		return "", fmt.Errorf("close component %s: %w", name, closeInputErr)
	}
	if closeOutputErr != nil {
		return "", fmt.Errorf("close cached component %s: %w", name, closeOutputErr)
	}
	if err := activateFile(temporaryPath, destination); err != nil {
		return "", fmt.Errorf("activate cached component %s: %w", name, err)
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return "", fmt.Errorf("secure cached component %s: %w", name, err)
	}
	current, _ := readManifest(directory)
	if current.Components == nil {
		current.Components = map[string]manifestEntry{}
	}
	current.Version = manifestVersion
	current.Release = release
	current.Platform = runtime.GOOS + "-" + runtime.GOARCH
	current.Components[name] = manifestEntry{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}
	if err := writeManifest(directory, current); err != nil {
		return "", err
	}
	return destination, nil
}

// Find returns a cached component only when its manifest, size, digest, and
// filesystem permissions still match.
func Find(release, name string) (string, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	directory, err := directoryFor(release)
	if err != nil {
		return "", err
	}
	name, err = safeSegment("component name", name)
	if err != nil {
		return "", err
	}
	return findLocked(directory, name)
}

func findLocked(directory, name string) (string, error) {
	current, err := readManifest(directory)
	if err != nil {
		return "", err
	}
	entry, ok := current.Components[name]
	if !ok {
		return "", os.ErrNotExist
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("cached component %s has unsafe type or permissions", name)
	}
	if info.Size() != entry.Size {
		return "", fmt.Errorf("cached component %s size does not match manifest", name)
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return "", err
	}
	if digest != entry.SHA256 {
		return "", fmt.Errorf("cached component %s checksum does not match manifest", name)
	}
	return path, nil
}

func directoryFor(release string) (string, error) {
	release, err := safeSegment("release", release)
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve component home: %w", err)
	}
	return filepath.Join(home, ".kubeloop", "components", release, runtime.GOOS+"-"+runtime.GOARCH), nil
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
	defer file.Close()
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
	defer os.Remove(temporaryPath)
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
	for attempt := 0; attempt < 100; attempt++ {
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
	defer file.Close()
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
