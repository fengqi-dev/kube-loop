package buildinfo

import (
	"runtime"
	"strings"
)

const (
	developmentVersion = "dev"
	unknownValue       = "unknown"
)

// These strings are the only linker injection points. Keep them unexported so
// application packages cannot mutate process-wide build metadata at runtime.
var (
	version     = developmentVersion
	commit      = unknownValue
	buildDate   = unknownValue
	protocolMin = "2.0"
	protocolMax = "2.0"
)

// Info describes the binary that is currently running.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	Compiler  string
	Platform  string
}

// ProtocolRange describes the independently versioned control-plane protocol.
type ProtocolRange struct {
	Min string
	Max string
}

// Get returns a snapshot of the current binary's build metadata.
func Get() Info {
	return Info{
		Version:   valueOr(version, developmentVersion),
		Commit:    valueOr(commit, unknownValue),
		BuildDate: valueOr(buildDate, unknownValue),
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// ControlPlaneProtocol returns the supported control-plane protocol range.
func ControlPlaneProtocol() ProtocolRange {
	return ProtocolRange{
		Min: valueOr(protocolMin, "2.0"),
		Max: valueOr(protocolMax, "2.0"),
	}
}

func valueOr(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
