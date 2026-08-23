package componentstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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
	//nolint:gosec // The private component directory needs owner execute permission for traversal.
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
	defer func() { _ = os.Remove(temporaryPath) }()
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
	//nolint:gosec // Cached components are executable only by their owner.
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
	// Windows FileMode permission bits do not represent the directory ACL. The
	// cache remains scoped to the per-user data root there; retain POSIX mode
	// enforcement on platforms where those bits are authoritative.
	unsafePermissions := runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || unsafePermissions {
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
