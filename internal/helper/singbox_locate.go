package helper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

// LocateBundledSingBox finds the packaged sing-box binary next to the desktop
// app or under the platform install root. It never copies the binary.
func LocateBundledSingBox() (string, error) {
	name := "sing-box"
	if runtime.GOOS == "windows" {
		name = "sing-box.exe"
	}
	var candidates []string
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
				return filepath.Clean(candidate), nil
			}
			return abs, nil
		}
	}
	return "", fmt.Errorf(
		"sing-box binary not found next to the application; run build helper-prebuild or install the full package",
	)
}

func configuredSingBoxPath(auth AuthFile) string {
	if auth.SingBoxPath != "" {
		return auth.SingBoxPath
	}
	return BundledSingBoxPath()
}

func resolveSingBoxPath(auth AuthFile) (string, error) {
	installer := &singboxdist.Installer{
		BundledPath:     configuredSingBoxPath(auth),
		DisableOverride: true,
		DisableDownload: true,
	}
	return installer.Ensure(context.Background())
}
