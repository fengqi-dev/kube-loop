package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func UserHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return home, nil
}

func UserDir() (string, error) {
	home, err := UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kubeloop"), nil
}

func TokenPath() (string, error) {
	dir, err := UserDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "helper.token"), nil
}

func SystemStateDir() string {
	return platformSystemStateDir()
}

func SystemTokenPath() string {
	return filepath.Join(SystemStateDir(), "helper.token")
}

func SystemAuthPath() string {
	return filepath.Join(SystemStateDir(), "helper.auth.json")
}

func BinaryInstallPath() string {
	return platformBinaryInstallPath()
}

// BinaryInstallPathForExecutable returns the helper install path selected by a
// packaged executable. On Windows the package can live outside Program Files,
// so callers that launch a separate install tool must derive the destination
// from that tool rather than from their own executable.
func BinaryInstallPathForExecutable(executable string) string {
	return platformBinaryInstallPathForExecutable(executable)
}

// BundledSingBoxPath returns the platform-package sing-box path when known.
func BundledSingBoxPath() string {
	return platformBundledSingBoxPath()
}

// CoreInstallPath is the protected system location used by the privileged
// helper. User-writable component caches must never be returned here.
func CoreInstallPath() string {
	return platformCoreInstallPath()
}

func SocketPath() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(SystemStateDir(), "helper.sock")
	default:
		if IsDevBuild() {
			return "/var/run/kubeloop-dev/helper.sock"
		}
		return "/var/run/kubeloop/helper.sock"
	}
}
