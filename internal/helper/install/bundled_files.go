package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	"github.com/fengqi-dev/kube-loop/internal/helper"
)

var (
	bundledFilesMu       sync.RWMutex
	bundledFiles         = map[string][]byte{}
	bundledHashes        = map[string]string{}
	materializeBundledMu sync.Mutex
)

// SetBundledBinary supplies the platform helper service embedded by a client.
// The standalone helper binary never calls it.
func SetBundledBinary(content []byte) {
	SetBundledFile(helperBinaryName(helperServiceName), content)
}

// SetBundledFile supplies a named runtime binary embedded by a client.
func SetBundledFile(name string, content []byte) {
	bundledFilesMu.Lock()
	defer bundledFilesMu.Unlock()
	if len(content) == 0 {
		delete(bundledFiles, name)
		delete(bundledHashes, name)
		return
	}
	bundledFiles[name] = bytes.Clone(content)
	sum := sha256.Sum256(content)
	bundledHashes[name] = hex.EncodeToString(sum[:])
}

func materializeBundledHelper() (string, bool, error) {
	return materializeBundledFile(helperBinaryName(helperServiceName))
}

func materializeBundledFile(name string) (string, bool, error) {
	materializeBundledMu.Lock()
	defer materializeBundledMu.Unlock()

	bundledFilesMu.RLock()
	content := bytes.Clone(bundledFiles[name])
	wantHash := bundledHashes[name]
	bundledFilesMu.RUnlock()
	if len(content) == 0 {
		return "", false, nil
	}
	if cachedPath, err := componentstore.Find(helper.Version, name); err == nil {
		cachedHash, hashErr := fileSHA256(cachedPath)
		if hashErr == nil && cachedHash == wantHash {
			return cachedPath, true, nil
		}
	}

	temp, err := os.CreateTemp("", ".kubeloop-component-*")
	if err != nil {
		return "", true, fmt.Errorf("create bundled helper: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o700); err != nil && runtime.GOOS != goosWindows {
		_ = temp.Close()
		return "", true, fmt.Errorf("make temporary helper executable: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return "", true, fmt.Errorf("write bundled helper: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", true, fmt.Errorf("close bundled helper: %w", err)
	}
	path, err := componentstore.Cache(helper.Version, name, tempPath)
	if err != nil {
		return "", true, fmt.Errorf("cache bundled helper: %w", err)
	}
	if wantHash != "" {
		actual, hashErr := fileSHA256(path)
		if hashErr != nil || actual != wantHash {
			return "", true, errors.New("cached bundled helper checksum does not match embedded content")
		}
	}
	return path, true, nil
}

func bundledHelperSHA256(source string) (string, error) {
	return bundledToolSHA256(helperBinaryName(helperServiceName), source)
}

func bundledToolSHA256(name, source string) (string, error) {
	bundledFilesMu.RLock()
	if hash := bundledHashes[name]; hash != "" {
		bundledFilesMu.RUnlock()
		return hash, nil
	}
	bundledFilesMu.RUnlock()
	return fileSHA256(source)
}

func helperBinaryName(base string) string {
	if runtime.GOOS == goosWindows {
		return base + ".exe"
	}
	return base
}

func singBoxBinaryName() string {
	return helperBinaryName("sing-box")
}
