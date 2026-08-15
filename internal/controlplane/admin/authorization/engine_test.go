package authorization

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestGroupAuthorizationModel(t *testing.T) {
	administratorGroup := uuid.NewString()
	developerGroup := uuid.NewString()
	engine, err := New(Snapshot{Version: CurrentVersion, Groups: []GroupAccess{
		{GroupID: administratorGroup, Administrator: true},
		{GroupID: developerGroup, Namespaces: []string{"development", "staging"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	administrator := Subject{ID: uuid.NewString(), Groups: []string{administratorGroup}}
	if decision := engine.Authorize(context.Background(), administrator, Request{
		Capability: "platform.overview.read",
	}); !decision.Allowed || decision.MatchingAllow[0].GroupID != administratorGroup {
		t.Fatalf("administrator decision = %+v", decision)
	}

	developer := Subject{ID: uuid.NewString(), Groups: []string{developerGroup}}
	if decision := engine.Authorize(context.Background(), developer, Request{
		Capability: CapabilityNamespaceAccess, Namespace: "development",
	}); !decision.Allowed {
		t.Fatalf("assigned namespace decision = %+v", decision)
	}
	if decision := engine.Authorize(context.Background(), developer, Request{
		Capability: CapabilityNamespaceAccess, Namespace: "production",
	}); decision.Allowed {
		t.Fatalf("unassigned namespace decision = %+v", decision)
	}
	if decision := engine.Authorize(context.Background(), developer, Request{
		Capability: "platform.overview.read",
	}); decision.Allowed {
		t.Fatalf("ordinary group received management access: %+v", decision)
	}
	if namespaces := engine.AuthorizedNamespaces(developer); !slices.Equal(namespaces, []string{"development", "staging"}) {
		t.Fatalf("delegated namespaces = %v", namespaces)
	}
}

func TestCapabilitiesReturnsStableDefensiveCatalog(t *testing.T) {
	first := Capabilities()
	second := Capabilities()
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("capabilities first=%v second=%v", first, second)
	}
	first[0] = "mutated"
	if second[0] == "mutated" || Capabilities()[0] == "mutated" {
		t.Fatal("Capabilities returned mutable shared storage")
	}
}
