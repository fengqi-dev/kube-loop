package authorization

import (
	"context"
	"errors"
	"sync"
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

func TestFiveBuiltInRolesEnforceExplicitPermissionMatrix(t *testing.T) {
	tests := []struct {
		name       string
		assignment Assignment
		allowed    Request
		denied     Request
	}{
		{
			name:       "platform admin",
			assignment: Assignment{ID: "platform", Role: RolePlatformAdmin, Subjects: []string{testPrincipalID}},
			allowed:    Request{Resource: ResourceAssignment, Operation: OperationCreate},
			denied:     Request{Resource: ResourceStatus, Operation: OperationDelete},
		},
		{
			name:       "security admin",
			assignment: Assignment{ID: "security", Role: RoleSecurityAdmin, Groups: []string{"security"}},
			allowed:    Request{Resource: ResourcePolicy, Operation: OperationPublish},
			denied:     Request{Resource: ResourceAssignment, Operation: OperationCreate},
		},
		{
			name:       "operator",
			assignment: Assignment{ID: "operator", Role: RoleOperator, Subjects: []string{testPrincipalID}},
			allowed:    Request{Resource: ResourceRelay, Operation: OperationDrain},
			denied:     Request{Resource: ResourcePolicy, Operation: OperationRead},
		},
		{
			name:       "auditor",
			assignment: Assignment{ID: "auditor", Role: RoleAuditor, Subjects: []string{testPrincipalID}},
			allowed:    Request{Resource: ResourceAudit, Operation: OperationList},
			denied:     Request{Resource: ResourceAudit, Operation: OperationExport},
		},
		{
			name: "namespace admin",
			assignment: Assignment{
				ID: "payments-admin", Role: RoleNamespaceAdmin, Subjects: []string{testPrincipalID},
				Namespaces: []string{"payments"},
			},
			allowed: Request{Resource: ResourceNamespacePolicy, Operation: OperationPublish, Namespace: "payments"},
			denied:  Request{Resource: ResourceNamespacePolicy, Operation: OperationPublish, Namespace: "other"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, err := New(Snapshot{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{test.assignment}})
			if err != nil {
				t.Fatal(err)
			}
			subject := Subject{ID: testPrincipalID, Groups: test.assignment.Groups}
			if len(test.assignment.Subjects) > 0 {
				subject.ID = test.assignment.Subjects[0]
			}
			decision := engine.Authorize(context.Background(), subject, test.allowed)
			if !decision.Allowed || decision.Role != test.assignment.Role || decision.AssignmentID != test.assignment.ID || decision.Revision != 1 {
				t.Fatalf("allowed decision = %#v", decision)
			}
			if decision := engine.Authorize(context.Background(), subject, test.denied); decision.Allowed {
				t.Fatalf("denied decision = %#v", decision)
			}
		})
	}
}

func TestNamespaceAdminCannotCrossScopeOrUseClusterOperations(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 7, Assignments: []Assignment{{
		ID: "team-a-admin", Role: RoleNamespaceAdmin, Groups: []string{"team-a"}, Namespaces: []string{"team-a"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	subject := Subject{ID: testPrincipalID, Groups: []string{"team-a"}}
	for _, request := range []Request{
		{Resource: ResourceNamespaceMember, Operation: OperationUpdate, Namespace: "team-b", ResourceName: "member-1"},
		{Resource: ResourceSession, Operation: OperationRead},
		{Resource: ResourceProvider, Operation: OperationRead},
		{Resource: ResourceSession, Operation: OperationStop, Namespace: "team-a", ResourceName: "session-1"},
	} {
		if decision := engine.Authorize(context.Background(), subject, request); decision.Allowed {
			t.Fatalf("cross-scope request %#v allowed by %#v", request, decision)
		}
	}
	if decision := engine.Authorize(context.Background(), subject, Request{
		Resource: ResourceSession, Operation: OperationRead, Namespace: "team-a", ResourceName: "session-1",
	}); !decision.Allowed || decision.Scope != "team-a" {
		t.Fatalf("delegated read decision = %#v", decision)
	}
}

func TestBootstrapFailsClosedRetiresAndRequiresExplicitRecovery(t *testing.T) {
	bootstrap := BootstrapConfig{Subjects: []string{testPrincipalID}, Groups: []string{"platform-bootstrap"}}
	request := Request{Resource: ResourceAssignment, Operation: OperationCreate}
	subject := Subject{ID: testPrincipalID}

	withoutState, err := NewDenyAll(WithBootstrap(bootstrap, nil))
	if err != nil {
		t.Fatal(err)
	}
	if decision := withoutState.Authorize(context.Background(), subject, request); decision.Reason != ReasonBootstrapStateUnavailable || decision.Allowed {
		t.Fatalf("missing state decision = %#v", decision)
	}

	state := &bootstrapStateStub{}
	engine, err := NewDenyAll(WithBootstrap(bootstrap, state))
	if err != nil {
		t.Fatal(err)
	}
	decision := engine.Authorize(context.Background(), subject, request)
	if !decision.Allowed || decision.Authentication != AuthenticationBootstrap || decision.Role != RolePlatformAdmin {
		t.Fatalf("active bootstrap decision = %#v", decision)
	}
	groupDecision := engine.Authorize(context.Background(), Subject{ID: testOtherPrincipalID, Groups: []string{"platform-bootstrap"}}, request)
	if !groupDecision.Allowed || groupDecision.Authentication != AuthenticationBootstrap {
		t.Fatalf("group bootstrap decision = %#v", groupDecision)
	}

	state.retired = true
	if decision := engine.Authorize(context.Background(), subject, request); decision.Reason != ReasonBootstrapRetired || decision.Allowed {
		t.Fatalf("retired bootstrap decision = %#v", decision)
	}
	recovery, err := NewDenyAll(WithBootstrap(BootstrapConfig{
		Subjects: []string{testPrincipalID}, RecoveryEnabled: true,
	}, state))
	if err != nil {
		t.Fatal(err)
	}
	if decision := recovery.Authorize(context.Background(), subject, request); !decision.Allowed || decision.Authentication != AuthenticationBootstrap {
		t.Fatalf("recovery bootstrap decision = %#v", decision)
	}
}

func TestFormalAssignmentPrecedesUnavailableBootstrapState(t *testing.T) {
	state := &bootstrapStateStub{err: errors.New("database unavailable")}
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{{
		ID: "formal-admin", Role: RolePlatformAdmin, Subjects: []string{testPrincipalID},
	}}}, WithBootstrap(BootstrapConfig{Subjects: []string{testPrincipalID}}, state))
	if err != nil {
		t.Fatal(err)
	}
	decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, Request{
		Resource: ResourceStatus, Operation: OperationRead,
	})
	if !decision.Allowed || decision.Authentication != AuthenticationNormal || decision.AssignmentID != "formal-admin" {
		t.Fatalf("formal decision = %#v", decision)
	}
}

func TestBreakGlassRequiresCurrentEnabledGeneration(t *testing.T) {
	state := &breakGlassStateStub{state: BreakGlassState{Enabled: true, Generation: "secret-generation-2"}}
	engine, err := NewDenyAll(WithBreakGlass(state))
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Resource: ResourceDiagnostic, Operation: OperationCreate}
	subject := Subject{
		ID: "break-glass", Authentication: AuthenticationBreakGlass,
		BreakGlassGeneration: "secret-generation-2",
	}
	decision := engine.Authorize(context.Background(), subject, request)
	if !decision.Allowed || decision.Authentication != AuthenticationBreakGlass || decision.Role != RolePlatformAdmin {
		t.Fatalf("current break-glass decision = %#v", decision)
	}
	subject.BreakGlassGeneration = "secret-generation-1"
	if decision := engine.Authorize(context.Background(), subject, request); decision.Allowed || decision.Reason != ReasonBreakGlassStale {
		t.Fatalf("stale break-glass decision = %#v", decision)
	}
	state.state.Enabled = false
	if decision := engine.Authorize(context.Background(), subject, request); decision.Allowed || decision.Reason != ReasonBreakGlassUnavailable {
		t.Fatalf("disabled break-glass decision = %#v", decision)
	}
}

func TestRevisionUpdateImmediatelyInvalidatesRemovedAssignment(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 10, Assignments: []Assignment{{
		ID: "reader", Role: RoleAuditor, Subjects: []string{testPrincipalID},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Resource: ResourceAudit, Operation: OperationRead}
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, request); !decision.Allowed || decision.Revision != 10 {
		t.Fatalf("initial decision = %#v", decision)
	}
	if err := engine.Update(Snapshot{Version: CurrentVersion, Revision: 11, Assignments: []Assignment{}}); err != nil {
		t.Fatal(err)
	}
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, request); decision.Allowed || decision.Revision != 11 {
		t.Fatalf("updated decision = %#v", decision)
	}
	if err := engine.Update(Snapshot{Version: CurrentVersion, Revision: 10, Assignments: []Assignment{}}); err == nil {
		t.Fatal("stale management policy update succeeded")
	}
}

func TestApplyAcceptsRollbackOnlyWithNewerActiveETag(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 10, Assignments: []Assignment{{
		ID: "reader-v10", Role: RoleAuditor, Subjects: []string{testPrincipalID},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if engine.ETag() != 0 {
		t.Fatalf("initial ETag = %d, want 0", engine.ETag())
	}
	if err := engine.Apply(Snapshot{Version: CurrentVersion, Revision: 5, Assignments: []Assignment{{
		ID: "reader-v5", Role: RoleAuditor, Subjects: []string{testPrincipalID},
	}}}, 2); err != nil {
		t.Fatal(err)
	}
	if engine.Revision() != 5 || engine.ETag() != 2 {
		t.Fatalf("active revision/ETag = %d/%d, want 5/2", engine.Revision(), engine.ETag())
	}
	engine.FailClosed()
	if engine.Available() {
		t.Fatal("failed-closed engine remained available")
	}
	if decision := engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, Request{
		Resource: ResourceAudit, Operation: OperationRead,
	}); decision.Allowed {
		t.Fatalf("failed-closed decision = %#v", decision)
	}
	if err := engine.Apply(Snapshot{Version: CurrentVersion, Revision: 5, Assignments: []Assignment{{
		ID: "reader-v5", Role: RoleAuditor, Subjects: []string{testPrincipalID},
	}}}, 2); err != nil {
		t.Fatalf("restore same ETag after fail-close: %v", err)
	}
	for _, staleETag := range []uint64{1, 2} {
		if err := engine.Apply(Snapshot{Version: CurrentVersion, Revision: 11, Assignments: []Assignment{}}, staleETag); err == nil {
			t.Fatalf("stale ETag %d succeeded", staleETag)
		}
	}
	if err := engine.Apply(Snapshot{Version: CurrentVersion, Revision: 11, Assignments: []Assignment{}}, 3); err != nil {
		t.Fatal(err)
	}
	if engine.Revision() != 11 || engine.ETag() != 3 {
		t.Fatalf("active revision/ETag = %d/%d, want 11/3", engine.Revision(), engine.ETag())
	}
}

func TestDryRunCannotManufactureBreakGlassContext(t *testing.T) {
	state := &breakGlassStateStub{state: BreakGlassState{Enabled: true, Generation: "generation-1"}}
	engine, err := NewDenyAll(WithBreakGlass(state))
	if err != nil {
		t.Fatal(err)
	}
	decision := engine.DryRun(context.Background(), Subject{
		ID: testPrincipalID, Authentication: AuthenticationBreakGlass, BreakGlassGeneration: "generation-1",
	}, Request{Resource: ResourceStatus, Operation: OperationRead})
	if decision.Allowed || decision.Authentication == AuthenticationBreakGlass {
		t.Fatalf("dry-run decision = %#v", decision)
	}
}

func TestLookupAuthorizedRejectsIDORBeforeRepositoryAccess(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 3, Assignments: []Assignment{{
		ID: "payments-admin", Role: RoleNamespaceAdmin, Subjects: []string{testPrincipalID}, Namespaces: []string{"payments"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	lookups := 0
	lookup := func(_ context.Context, namespace, resourceName string) (string, error) {
		lookups++
		return namespace + "/" + resourceName, nil
	}
	request := Request{
		Resource: ResourceNamespaceMember, Operation: OperationRead, Namespace: "other", ResourceName: "member-1",
	}
	if _, decision, err := LookupAuthorized(context.Background(), engine, Subject{ID: testPrincipalID}, request, lookup); !errors.Is(err, ErrForbidden) || decision.Allowed || lookups != 0 {
		t.Fatalf("cross-tenant lookup: decision=%#v error=%v lookups=%d", decision, err, lookups)
	}
	request.Namespace = "payments"
	value, decision, err := LookupAuthorized(context.Background(), engine, Subject{ID: testPrincipalID}, request, lookup)
	if err != nil || !decision.Allowed || value != "payments/member-1" || lookups != 1 {
		t.Fatalf("authorized lookup: value=%q decision=%#v error=%v lookups=%d", value, decision, err, lookups)
	}
}

func TestInvalidAssignmentsAndSelectorsAreRejected(t *testing.T) {
	tests := []Snapshot{
		{Version: CurrentVersion, Assignments: []Assignment{{ID: "no-revision", Role: RoleAuditor, Subjects: []string{testPrincipalID}}}},
		{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{{ID: "wildcard", Role: RoleAuditor, Subjects: []string{"*"}}}},
		{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{{ID: "unknown", Role: "root", Subjects: []string{testPrincipalID}}}},
		{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{{ID: "missing-scope", Role: RoleNamespaceAdmin, Subjects: []string{testPrincipalID}}}},
		{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{{
			ID: "cluster-with-scope", Role: RolePlatformAdmin, Subjects: []string{testPrincipalID}, Namespaces: []string{"payments"},
		}}},
	}
	for index, snapshot := range tests {
		if _, err := New(snapshot); err == nil {
			t.Fatalf("invalid snapshot %d succeeded", index)
		}
	}
	if _, err := NewDenyAll(WithBootstrap(BootstrapConfig{Subjects: []string{"*"}}, &bootstrapStateStub{})); err == nil {
		t.Fatal("wildcard bootstrap subject succeeded")
	}
}

func TestConcurrentAuthorizationAndRevisionUpdate(t *testing.T) {
	engine, err := New(Snapshot{Version: CurrentVersion, Revision: 1, Assignments: []Assignment{{
		ID: "reader", Role: RoleAuditor, Subjects: []string{testPrincipalID},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			for range 500 {
				engine.Authorize(context.Background(), Subject{ID: testPrincipalID}, Request{Resource: ResourceAudit, Operation: OperationRead})
			}
		})
	}
	for revision := uint64(2); revision < 100; revision++ {
		assignments := []Assignment{}
		if revision%2 == 0 {
			assignments = []Assignment{{ID: "reader", Role: RoleAuditor, Subjects: []string{testPrincipalID}}}
		}
		if err := engine.Update(Snapshot{Version: CurrentVersion, Revision: revision, Assignments: assignments}); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
}
