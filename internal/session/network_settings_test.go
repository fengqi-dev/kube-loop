package session

import (
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/store"
)

func TestSetHostAliasesUpdatesConnectedCoreAndStore(t *testing.T) {
	stateStore, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	core := newFakeCore()
	manager := NewManager(&fakeProvider{}, WithStore(stateStore))
	manager.mu.Lock()
	manager.runningCore = core.process
	manager.mu.Unlock()
	manager.publish(State{Phase: PhaseConnected, Context: "dev"})

	items := []store.HostAliasSpec{{Domain: "api.example.test", IP: "192.0.2.10"}}
	if err := manager.SetHostAliases("dev", items); err != nil {
		t.Fatal(err)
	}
	if got := core.process.updatedHosts; len(got) != 1 ||
		got[0].Domain != items[0].Domain || got[0].IP != items[0].IP {
		t.Fatalf("active aliases = %#v, want %#v", got, items)
	}
	if got := manager.HostAliases("dev"); len(got) != 1 || got[0] != items[0] {
		t.Fatalf("stored aliases = %#v, want %#v", got, items)
	}

	if err := manager.SetHostAliases("dev", nil); err != nil {
		t.Fatal(err)
	}
	if len(core.process.updatedHosts) != 0 {
		t.Fatalf("active aliases after clear = %#v", core.process.updatedHosts)
	}
	if got := manager.HostAliases("dev"); len(got) != 0 {
		t.Fatalf("stored aliases after clear = %#v", got)
	}

	_ = core.process.Close()
	<-core.process.Done()
}
