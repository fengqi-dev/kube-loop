package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

// LocateBundledSingBox finds the packaged sing-box binary next to the desktop
// app or under the platform install root. It never copies the binary.
func LocateBundledSingBox() (string, error) {
	name := "sing-box"
	if runtime.GOOS == goosWindows {
		name = "sing-box.exe"
	}
	var candidates []string
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

func configuredSingBoxPath(auth AuthFile) string {
	if auth.SingBoxPath != "" {
		return auth.SingBoxPath
	}
	return BundledSingBoxPath()
}

func resolveSingBoxPath(auth AuthFile) (string, error) {
	executable, _ := os.Executable()
	if executable != "" {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
	}
	candidates := trustedSingBoxCandidates(auth, executable)
	for _, candidate := range candidates {
		if path, err := validateTrustedSingBox(candidate); err == nil {
			return path, nil
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"bundled sing-box %s not found; reinstall the KubeLoop package",
			singboxdist.Version,
		)
	}
	return "", fmt.Errorf(
		"bundled sing-box %s not found (tried %s); reinstall the KubeLoop package",
		singboxdist.Version,
		strings.Join(candidates, ", "),
	)
}

func trustedSingBoxCandidates(auth AuthFile, executable string) []string {
	var candidates []string
	if configured := configuredSingBoxPath(auth); configured != "" {
		candidates = append(candidates, configured)
	}
	if executable == "" {
		return candidates
	}
	name := "sing-box"
	if runtime.GOOS == goosWindows {
		name = "sing-box.exe"
	}
	dir := filepath.Dir(executable)
	return append(candidates,
		filepath.Join(dir, name),
		filepath.Join(dir, "..", name),
		filepath.Join(dir, "Resources", name),
		filepath.Join(dir, "..", "Resources", name),
	)
}

func validateTrustedSingBox(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("find sing-box binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("sing-box binary is not a regular file")
	}
	if runtime.GOOS != goosWindows && info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("sing-box binary is not executable")
	}
	return filepath.Clean(path), nil
}
