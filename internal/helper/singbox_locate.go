package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
)

// LocateBundledSingBox finds the packaged sing-box binary next to the desktop
// app or under the platform install root. It never copies the binary.
func LocateBundledSingBox() (string, error) {
	name := "sing-box"
	if runtime.GOOS == goosWindows {
		name = "sing-box.exe"
	}
	var candidates []string
	// A dev release name is intentionally stable, so its component cache can
	// contain yesterday's binary after source or registry patches change.
	// Prefer the asset rebuilt by `wails dev` when running from the repository.
	if IsDevBuild() {
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, "build", "bin", name))
		}
	}
	if path, err := componentstore.Find(Version, name); err == nil {
		candidates = append(candidates, path)
	}
	if path := BundledSingBoxPath(); path != "" {
		candidates = append(candidates, path)
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, name),
			filepath.Join(dir, "resources", name),
			filepath.Join(dir, "Resources", name),
			filepath.Join(dir, "..", "Resources", name),
			filepath.Join(dir, "..", name),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "build", "bin", name),
			filepath.Join(cwd, name),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			abs, absErr := filepath.Abs(candidate)
			if absErr != nil {
				//nolint:nilerr // A clean relative path remains usable when Abs fails.
				return filepath.Clean(candidate), nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf(
		"sing-box binary not found next to the application; run build helper-prebuild or install the full package",
	)
}
