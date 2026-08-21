package helper

// Version is the helper protocol/binary version. Overridden at link time.
var Version = developmentVersion

const (
	developmentVersion = "dev"
	goosDarwin         = "darwin"
	goosWindows        = "windows"

	// ServiceLabelRelease is the launchd label / privileged helper tool name for release builds.
	ServiceLabelRelease = "dev.fengqi.kubeloop.helper"
	// ServiceLabelDev is used by wails/local "dev" builds so they never collide with release.
	ServiceLabelDev = "dev.fengqi.kubeloop.helper.dev"

	ServiceNameWinRelease = "KubeLoopHelper"
	ServiceNameWinDev     = "KubeLoopHelperDev"

	ProductName = "KubeLoop"
)

// IsDevBuild reports whether this binary is a development (non-release) build.
// Release CI injects a version tag via -X main.version=...; local/wails defaults stay "dev".
func IsDevBuild() bool {
	return Version == "" || Version == developmentVersion
}

// ServiceLabel is the macOS launchd label and PrivilegedHelperTools basename.
func ServiceLabel() string {
	if IsDevBuild() {
		return ServiceLabelDev
	}
	return ServiceLabelRelease
}

// ServiceNameWin is the Windows SCM service name.
func ServiceNameWin() string {
	if IsDevBuild() {
		return ServiceNameWinDev
	}
	return ServiceNameWinRelease
}

// ServiceDisplayName is the human-readable Windows / systemd description title.
func ServiceDisplayName() string {
	if IsDevBuild() {
		return "KubeLoop Helper (dev)"
	}
	return "KubeLoop Helper"
}

// InstallProductDir is the Program Files / ProgramData folder name.
func InstallProductDir() string {
	if IsDevBuild() {
		return ProductName + "-Dev"
	}
	return ProductName
}

// HelperBinaryBaseName is the on-disk helper executable basename without extension.
//
//nolint:revive // HelperBinaryBaseName preserves the established public helper API.
func HelperBinaryBaseName() string {
	if IsDevBuild() {
		return "kubeloop-helper-dev"
	}
	return "kubeloop-helper"
}

// SystemdUnitName is the Linux unit filename (with .service).
func SystemdUnitName() string {
	return HelperBinaryBaseName() + ".service"
}

// HelperLogPath is the launchd stdout/stderr log path on macOS.
//
//nolint:revive // HelperLogPath preserves the established public helper API.
func HelperLogPath() string {
	if IsDevBuild() {
		return "/var/log/kubeloop-helper-dev.log"
	}
	return "/var/log/kubeloop-helper.log"
}
