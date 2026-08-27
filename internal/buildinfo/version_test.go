package buildinfo

import (
	"runtime"
	"testing"
)

func TestGetIncludesRuntimeMetadata(t *testing.T) {
	info := Get()
	if info.Version == "" || info.Commit == "" || info.BuildDate == "" {
		t.Fatalf("incomplete build info: %#v", info)
	}
	if info.GoVersion != runtime.Version() || info.Compiler != runtime.Compiler {
		t.Fatalf("runtime metadata = %#v", info)
	}
	if info.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("platform = %q", info.Platform)
	}
}

func TestControlPlaneProtocolIsIndependent(t *testing.T) {
	protocol := ControlPlaneProtocol()
	if protocol.Min == "" || protocol.Max == "" {
		t.Fatalf("protocol range = %#v", protocol)
	}
}
