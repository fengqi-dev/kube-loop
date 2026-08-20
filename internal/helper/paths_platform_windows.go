//go:build windows

package helper

import (
	"os"
	"path/filepath"

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

func platformBundledSingBoxPath() string {
	executable, err := os.Executable()
	if err != nil {
		return filepath.Join(windowsProgramFilesProductRoot(), "sing-box.exe")
	}
	return filepath.Join(platformInstallRootForExecutable(executable), "sing-box.exe")
}

func platformCoreInstallPath() string {
	return filepath.Join(windowsProgramFilesProductRoot(), "sing-box.exe")
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
