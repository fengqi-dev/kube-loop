package helperd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

const goosWindows = "windows"

func configuredSingBoxPath(auth helper.AuthFile) string {
	if auth.SingBoxPath != "" {
		return auth.SingBoxPath
	}
	return helper.BundledSingBoxPath()
}

func resolveSingBoxPath(auth helper.AuthFile) (string, error) {
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

func trustedSingBoxCandidates(auth helper.AuthFile, executable string) []string {
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
