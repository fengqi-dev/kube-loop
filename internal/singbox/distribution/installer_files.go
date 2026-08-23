package distribution

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fengqi-dev/kube-loop/internal/userpaths"
)

func writeExecutable(binaryPath string, executable []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(binaryPath), ".sing-box-*")
	if err != nil {
		return fmt.Errorf("create temporary sing-box binary: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if runtime.GOOS != windowsGOOS {
		if err := temp.Chmod(0o755); err != nil {
			return fmt.Errorf("set sing-box permissions: %w", errors.Join(err, temp.Close()))
		}
	}
	if _, err := temp.Write(executable); err != nil {
		return fmt.Errorf("write sing-box binary: %w", errors.Join(err, temp.Close()))
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync sing-box binary: %w", errors.Join(err, temp.Close()))
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close sing-box binary: %w", err)
	}
	if err := os.Rename(tempPath, binaryPath); err != nil {
		return fmt.Errorf("install sing-box binary: %w", err)
	}
	return nil
}

func (i *Installer) platform() (string, string) {
	goos, goarch := i.GOOS, i.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (i *Installer) baseDir() (string, error) {
	if i.BaseDir != "" {
		return i.BaseDir, nil
	}
	layout, err := userpaths.Default()
	if err != nil {
		return "", err
	}
	return layout.CacheDir(), nil
}

func (i *Installer) bundledCandidates() []string {
	var candidates []string
	if i.BundledPath != "" {
		candidates = append(candidates, i.BundledPath)
	}
	goos, _ := i.platform()
	name := singBoxBinary
	if goos == windowsGOOS {
		name = singBoxBinaryWin
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, name),              // next to helper service / app
			filepath.Join(dir, "..", name),        // resources/../sing-box
			filepath.Join(dir, "Resources", name), // macOS app Resources
			filepath.Join(dir, "..", "Resources", name),
		)
	}
	return candidates
}

func validateBinary(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("find sing-box binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("sing-box binary is not a regular file")
	}
	if runtime.GOOS != windowsGOOS && info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("sing-box binary is not executable")
	}
	return filepath.Clean(path), nil
}
