package authorization

import (
	"context"
	"errors"
	"testing"
)

const (
	testPrincipalID      = "00000000-0000-4000-8000-000000000001"
	testOtherPrincipalID = "00000000-0000-4000-8000-000000000002"
)

type bootstrapStateStub struct {
	retired bool
	err     error
}

func (state *bootstrapStateStub) BootstrapRetired(context.Context) (bool, error) {
	return state.retired, state.err
}

type breakGlassStateStub struct {
	state BreakGlassState
	err   error
}

func (state *breakGlassStateStub) CurrentBreakGlassState(context.Context) (BreakGlassState, error) {
	return state.state, state.err
}

func principalBinding(id string, role Role, principal string, scope BindingScope) Binding {
	return Binding{ID: id, Subject: SubjectRef{Type: SubjectPrincipal, PrincipalID: principal}, RoleID: role, Scope: scope, ManagedBy: ManagedByPlatform}
}

func TestUnifiedPolicyExplicitDenyOverridesEveryAllow(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 1,
		Roles: []RoleDefinition{
			{ID: "exec-allow", DisplayName: "Exec allow", Statements: []Statement{{Effect: EffectAllow, Capabilities: []Capability{CapabilityNamespaceAccess, "namespace.exec.open"}}}},
			{ID: "exec-deny", DisplayName: "Exec deny", Statements: []Statement{{Effect: EffectDeny, Capabilities: []Capability{"namespace.exec.open"}}}},
		},
		Bindings: []Binding{
			principalBinding("allow", "exec-allow", testPrincipalID, BindingScope{Type: ScopeNamespaces, Names: []string{"team-a"}}),
			principalBinding("deny", "exec-deny", testPrincipalID, BindingScope{Type: ScopeNamespaces, Names: []string{"team-a"}}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, Request{Capability: "namespace.exec.open", Namespace: "team-a", LabelsAvailable: true})
	if decision.Allowed || decision.Reason != ReasonExplicitDeny || len(decision.MatchingAllow) == 0 || len(decision.MatchingDeny) != 1 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestProviderScopedGroupAndNamespaceSelectors(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 2, Bindings: []Binding{{
		ID: "oidc-team", Subject: SubjectRef{Type: SubjectGroup, ProviderID: "auth0", GroupName: "developers"},
		RoleID: RoleNamespaceViewer, Scope: BindingScope{Type: ScopeNamespaces, LabelSelectors: []NamespaceSelector{{MatchLabels: map[string]string{"team": "payments"}}}}, ManagedBy: ManagedByPlatform,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Capability: "namespace.resources.read", Namespace: "payments-prod", NamespaceLabels: map[string]string{"team": "payments"}, LabelsAvailable: true}
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID, Provider: "auth0", Groups: []string{"developers"}}, request); !decision.Allowed {
		t.Fatalf("matching group decision = %#v", decision)
	}
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID, Provider: "other", Groups: []string{"developers"}}, request); decision.Allowed {
		t.Fatalf("cross-provider group allowed = %#v", decision)
	}
	request.LabelsAvailable = false
	request.NamespaceLabels = nil
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID, Provider: "auth0", Groups: []string{"developers"}}, request); decision.Reason != ReasonScopeUnavailable {
		t.Fatalf("missing labels decision = %#v", decision)
	}
}

func TestDelegatedBindingsCannotEscalate(t *testing.T) {
	tests := []Binding{
		{ID: "platform", Subject: SubjectRef{Type: SubjectPrincipal, PrincipalID: testPrincipalID}, RoleID: RolePlatformAdmin, Scope: BindingScope{Type: ScopePlatform}, ManagedBy: ManagedByDelegated},
		{ID: "selector", Subject: SubjectRef{Type: SubjectPrincipal, PrincipalID: testPrincipalID}, RoleID: RoleNamespaceViewer, Scope: BindingScope{Type: ScopeNamespaces, LabelSelectors: []NamespaceSelector{{MatchLabels: map[string]string{"team": "a"}}}}, ManagedBy: ManagedByDelegated},
		{ID: "admin", Subject: SubjectRef{Type: SubjectPrincipal, PrincipalID: testPrincipalID}, RoleID: RoleNamespaceAdmin, Scope: BindingScope{Type: ScopeNamespaces, Names: []string{"team-a"}}, ManagedBy: ManagedByDelegated},
	}
	for _, binding := range tests {
		if _, err := New(Snapshot{Version: CurrentVersion, Revision: 1, Bindings: []Binding{binding}}); err == nil {
			t.Fatalf("escalating delegated binding accepted: %#v", binding)
		}
	}
}

func TestBootstrapBreakGlassAndFailClosed(t *testing.T) {
	request := Request{Capability: "platform.authorization.manage"}
	state := &bootstrapStateStub{}
	engine, err := NewDenyAll(WithBootstrap(BootstrapConfig{Subjects: []string{testPrincipalID}}, state))
	if err != nil {
		t.Fatal(err)
	}
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, request); !decision.Allowed || decision.Authentication != AuthenticationBootstrap {
		t.Fatalf("bootstrap = %#v", decision)
	}
	state.retired = true
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, request); decision.Reason != ReasonBootstrapRetired {
		t.Fatalf("retired = %#v", decision)
	}
	breakGlass := &breakGlassStateStub{state: BreakGlassState{Enabled: true, Generation: "generation"}}
	engine, _ = NewDenyAll(WithBreakGlass(breakGlass))
	if decision := engine.Authorize(context.Background(), Subject{ID: "break-glass", Authentication: AuthenticationBreakGlass, BreakGlassGeneration: "generation"}, request); !decision.Allowed {
		t.Fatalf("break-glass = %#v", decision)
	}
	breakGlass.err = errors.New("unavailable")
	if decision := engine.Authorize(context.Background(), Subject{ID: "break-glass", Authentication: AuthenticationBreakGlass, BreakGlassGeneration: "generation"}, request); decision.Allowed {
		t.Fatalf("unavailable break-glass = %#v", decision)
	}
}

func TestPlatformBindingCoversNamespaceCapabilities(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 3, Bindings: []Binding{principalBinding("admin", RolePlatformAdmin, testPrincipalID, BindingScope{Type: ScopePlatform})}})
	if err != nil {
		t.Fatal(err)
	}
	decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, Request{Capability: "namespace.port-forward.open", Namespace: "team-a", LabelsAvailable: true})
	if !decision.Allowed {
		t.Fatalf("platform namespace decision = %#v", decision)
	}
	engine.FailClosed()
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, Request{Capability: "platform.overview.read"}); decision.Allowed {
		t.Fatalf("fail closed = %#v", decision)
	}
}
