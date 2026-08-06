package restoreplan

import (
	"reflect"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/store"
)

func TestBuildStartupDefersReconnectContext(t *testing.T) {
	snapshot := store.State{
		UI: store.UIState{LastContext: "connected", LastNamespace: "fallback"},
		Clusters: map[string]*store.ClusterState{
			"connected": {
				Connected:      true,
				ConnectionMode: "socks",
				PortForwards:   []store.PortForwardSpec{{Name: "deferred"}},
			},
			"detached": {
				PortForwards: []store.PortForwardSpec{{Name: "ready"}},
			},
		},
	}
	plan := BuildStartup(snapshot)
	if plan.Reconnect == nil ||
		plan.Reconnect.Context != "connected" ||
		plan.Reconnect.Namespace != "fallback" ||
		plan.Reconnect.Mode != "socks" {
		t.Fatalf("reconnect = %#v", plan.Reconnect)
	}
	want := []PortForward{{Context: "detached", Spec: store.PortForwardSpec{Name: "ready"}}}
	if !reflect.DeepEqual(plan.PortForwards, want) {
		t.Fatalf("port-forwards = %#v, want %#v", plan.PortForwards, want)
	}
}

func TestBuildStartupNormalizesReconnectDefaults(t *testing.T) {
	plan := BuildStartup(store.State{
		UI: store.UIState{LastContext: "cluster"},
		Clusters: map[string]*store.ClusterState{
			"cluster": {Connected: true, ConnectionMode: "unknown"},
		},
	})
	if plan.Reconnect == nil ||
		plan.Reconnect.Namespace != "default" ||
		plan.Reconnect.Mode != "tun" {
		t.Fatalf("reconnect = %#v", plan.Reconnect)
	}
}
