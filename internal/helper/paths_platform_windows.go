//go:build windows

package helper

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func platformSystemStateDir() string {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, InstallProductDir())
}

func platformBinaryInstallPath() string {
	executable, err := os.Executable()
	if err != nil {
		return filepath.Join(
			windowsProgramFilesProductRoot(),
			"resources",
			HelperBinaryBaseName()+".exe",
		)
	}
	return platformBinaryInstallPathForExecutable(executable)
}

func platformBinaryInstallPathForExecutable(executable string) string {
	return filepath.Join(
		platformInstallRootForExecutable(executable),
		"resources",
		HelperBinaryBaseName()+".exe",
	)
}

// platformLegacyBinaryInstallPath is the pre-resources helper location under Program Files.
func platformLegacyBinaryInstallPath() string {
	return filepath.Join(windowsProgramFilesProductRoot(), HelperBinaryBaseName()+".exe")
}

func platformBundledSingBoxPath() string {
	executable, err := os.Executable()
	if err != nil {
		return filepath.Join(windowsProgramFilesProductRoot(), "sing-box.exe")
	}
	return filepath.Join(platformInstallRootForExecutable(executable), "sing-box.exe")
}

func platformInstallRootForExecutable(executable string) string {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	if root := installRootFromWindowsExe(executable); root != "" {
		return root
	}
	return windowsProgramFilesProductRoot()
}

func windowsProgramFilesProductRoot() string {
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, windows.KF_FLAG_DEFAULT)
	if err != nil {
		root = `C:\Program Files`
	}
	return filepath.Join(root, InstallProductDir())
}

// windowsDisplacedHelperPaths are older fixed Program Files locations to remove
// after the helper moves with a custom install directory (e.g. D:\KubeLoop).
func windowsDisplacedHelperPaths(current string) []string {
	root := windowsProgramFilesProductRoot()
	candidates := []string{
		filepath.Join(root, HelperBinaryBaseName()+".exe"),
		filepath.Join(root, "resources", HelperBinaryBaseName()+".exe"),
	}
	out := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if current != "" && strings.EqualFold(filepath.Clean(path), filepath.Clean(current)) {
			continue
		}
		out = append(out, path)
	}
	return out
}

// WindowsDisplacedHelperPaths returns legacy helper locations that may be
// removed after installing into the current application root.
func WindowsDisplacedHelperPaths(current string) []string {
	return windowsDisplacedHelperPaths(current)
}
