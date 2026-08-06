//go:build !windows

package helper

import (
	"runtime"
)

func platformSystemStateDir() string {
	if IsDevBuild() {
		return "/var/lib/kubeloop-dev"
	}
	return "/var/lib/kubeloop"
}

func platformBinaryInstallPath() string {
	if runtime.GOOS == "darwin" {
		return "/Library/PrivilegedHelperTools/" + ServiceLabel()
	}
	return "/usr/local/libexec/" + HelperBinaryBaseName()
}

func platformBinaryInstallPathForExecutable(string) string {
	return platformBinaryInstallPath()
}

func platformLegacyBinaryInstallPath() string {
	return ""
}

func platformBundledSingBoxPath() string {
	switch runtime.GOOS {
	case "darwin":
		return ""
	case "linux":
		if IsDevBuild() {
			return "/usr/lib/kubeloop-dev/sing-box"
		}
		return "/usr/lib/kubeloop/sing-box"
	default:
		return ""
	}
}
