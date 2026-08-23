package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	"github.com/fengqi-dev/kube-loop/internal/helper"
)

const (
	helperServiceName     = "kubeloop-helper"
	supervisorServiceName = "kubeloop-supervisor"
)

func LocateBundledHelper() (string, error) {
	return locateBundledTool(helperServiceName)
}

func LocateBundledSupervisor() (string, error) {
	return locateBundledTool(supervisorServiceName)
}

func locateBundledTool(baseName string) (string, error) {
	name := helperBinaryName(baseName)
	// Unix helpers are installed outside the application bundle. Always
	// materialize the exact bytes embedded in this desktop build first so stale
	// development/package artifacts cannot be selected as the privileged source.
	if runtime.GOOS != goosWindows {
		if path, ok, err := materializeBundledFile(name); ok || err != nil {
			return path, err
		}
	}
	if path, err := componentstore.Find(helper.Version, name); err == nil {
		return path, nil
	}
	// Prefer on-disk package resources ({installRoot}\resources) over
	// materializing the embedded copy — avoids multi-MB read/write on every elevate.
	if path, err := findBundledToolOnDisk(name); err == nil {
		return path, nil
	}
	if path, ok, err := materializeBundledFile(name); ok || err != nil {
		return path, err
	}
	if path, lookErr := exec.LookPath(name); lookErr == nil {
		return path, nil
	}
	exe, _ := os.Executable()
	return "", fmt.Errorf("%s binary not found near %s", name, exe)
}

func findBundledToolOnDisk(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	candidates := bundledToolCandidates(exe, cwd, name)
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate)
		if statErr == nil && !info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", fmt.Errorf("resolve bundled %s path: %w", name, absErr)
			}
			return filepath.Clean(absolute), nil
		}
	}
	return "", fmt.Errorf("%s not found on disk", name)
}

func bundledToolCandidates(exe, cwd, name string) []string {
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "resources", name),
		filepath.Join(dir, "Resources", name),
		filepath.Join(dir, "..", "Resources", name),
		filepath.Join(dir, name),
		filepath.Join(dir, "Helpers", name),
		filepath.Join(dir, "..", "Helpers", name),
		filepath.Join("build", "bin", "resources", name),
		filepath.Join("build", "bin", "Resources", name),
		filepath.Join("build", "bin", name),
	}
	// On Windows the packaged helper is also the service install target under
	// the application's resources directory. On macOS and Linux, however,
	// BinaryInstallPath points to the already-installed privileged helper. It
	// must never be treated as the bundled upgrade source.
	if runtime.GOOS == goosWindows {
		installRoot := filepath.Dir(helper.BinaryInstallPath())
		candidates = append([]string{
			filepath.Join(installRoot, name),
			filepath.Join(filepath.Dir(installRoot), "resources", name),
		}, candidates...)
	}
	if cwd != "" {
		candidates = append(candidates,
			filepath.Join(cwd, "build", "bin", "resources", name),
			filepath.Join(cwd, "build", "bin", "Resources", name),
			filepath.Join(cwd, "build", "bin", name),
			filepath.Join(cwd, name),
		)
	}
	return candidates
}
