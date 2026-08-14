package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/google/uuid"
)

func TestNamespaceDelegationAuthorizationIsExactAndCannotCrossScope(t *testing.T) {
	principalID := uuid.NewString()
	engine, err := adminauthorization.New(adminauthorization.Snapshot{
		Version:  adminauthorization.CurrentVersion,

		Bindings: []adminauthorization.Binding{{
			ID: uuid.NewString(),
			Subject: adminauthorization.SubjectRef{
				Type: adminauthorization.SubjectPrincipal, PrincipalID: principalID,
			},
			RoleID: adminauthorization.RoleNamespaceAdmin,
			Scope: adminauthorization.BindingScope{
				Type: adminauthorization.ScopeNamespaces, Names: []string{"team-a"},
			},
			ManagedBy: adminauthorization.ManagedByPlatform,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	api := &readAPI{authorizer: engine}
	request := httptest.NewRequest("GET", "/authorization/delegations", nil)
	request = request.WithContext(context.WithValue(request.Context(), subjectContextKey, adminauthorization.Subject{ID: principalID}))
	if !api.requireNamespaceAuthorization(request, "team-a", adminauthorization.OperationCreate) {
		t.Fatal("namespace administrator could not delegate inside its exact namespace")
	}
	if api.requireNamespaceAuthorization(request, "team-b", adminauthorization.OperationCreate) {
		t.Fatal("namespace administrator delegated outside its exact namespace")
	}
	if api.requireNamespaceAuthorization(request, "", adminauthorization.OperationCreate) {
		t.Fatal("empty namespace was accepted for delegation")
	}
}
