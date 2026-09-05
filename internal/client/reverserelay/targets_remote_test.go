package reverserelay

import (
	"reflect"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func TestTargetConversions(t *testing.T) {
	local := []Target{
		{ServicePort: 80, Protocol: "tcp", LocalHost: "localhost", LocalPort: 8080},
		{ServicePort: 53, Protocol: "udp", LocalHost: "", LocalPort: 5353},
	}
	want := []remote.LocalTarget{
		{Protocol: "tcp", ServicePort: 80, LocalHost: "localhost", LocalPort: 8080},
		{Protocol: "udp", ServicePort: 53, LocalHost: "", LocalPort: 5353},
	}
	wire := RemoteTargets(local)
	if !reflect.DeepEqual(wire, want) {
		t.Fatalf("remote targets = %#v, want %#v", wire, want)
	}
	restored := LocalTargets(wire)
	if !reflect.DeepEqual(restored, local) {
		t.Fatalf("local targets = %#v, want %#v", restored, local)
	}
	wire[0].LocalHost = "changed"
	restored[1].LocalPort = 1
	if local[0].LocalHost != "localhost" || local[1].LocalPort != 5353 || restored[0].LocalHost != "localhost" || wire[1].LocalPort != 5353 {
		t.Fatal("conversions must return independent copies")
	}
	for _, input := range [][]Target{nil, {}} {
		if got := RemoteTargets(input); got == nil || len(got) != 0 {
			t.Fatalf("empty remote conversion = %#v", got)
		}
	}
	for _, input := range [][]remote.LocalTarget{nil, {}} {
		if got := LocalTargets(input); got == nil || len(got) != 0 {
			t.Fatalf("empty local conversion = %#v", got)
		}
	}
}
