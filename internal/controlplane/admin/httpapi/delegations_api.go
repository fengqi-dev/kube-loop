package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) listDelegationPrincipals(ctx *echo.Context) error {
	namespace := strings.TrimSpace(ctx.QueryParam("namespace"))
	request := ctx.Request()
	if !api.requireNamespaceAuthorization(request, namespace, adminauthorization.OperationRead) {
		writeError(ctx.Response(), http.StatusForbidden, "forbidden", "namespace authorization is not permitted", requestID(request))
		return nil
	}
	principals, err := api.status.Principals().List(request.Context(), storage.PrincipalListFilter{Limit: 100})
	if err != nil {
		writeError(ctx.Response(), http.StatusServiceUnavailable, "unavailable", "principal directory is unavailable", requestID(request))
		return nil
	}
	items := make([]principalDocument, 0, len(principals))
	for _, principal := range principals {
		items = append(items, principalDocument{
			ID: principal.ID, Provider: principal.Provider, DisplayName: principal.DisplayName,
			Email: principal.Email, Groups: slices.Clone(principal.Groups),
			CreatedAt: principal.CreatedAt, UpdatedAt: principal.UpdatedAt,
		})
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *readAPI) listDelegations(ctx *echo.Context) error {
	namespace := strings.TrimSpace(ctx.QueryParam("namespace"))
	request := ctx.Request()
	if !api.requireNamespaceAuthorization(request, namespace, adminauthorization.OperationRead) {
		writeError(ctx.Response(), http.StatusForbidden, "forbidden", "namespace authorization is not permitted", requestID(request))
		return nil
	}
	state, err := api.policy.CurrentPolicy(request.Context())
	if err != nil {
		writePolicyError(ctx.Response(), request, err)
		return nil
	}
	items := make([]adminauthorization.Binding, 0)
	for _, binding := range state.Snapshot.Bindings {
		if delegationOwnsNamespace(binding, namespace) {
			items = append(items, binding)
		}
	}
	ctx.Response().Header().Set("ETag", strongETag(state.Pointer.ETag))
	return ctx.JSON(http.StatusOK, map[string]any{
		"namespace": namespace, "etag": state.Pointer.ETag, "revision": state.Snapshot.Revision,
		"bindings": items, "roles": delegatableRoles(state.Snapshot.Roles),
	})
}

func (api *readAPI) putDelegation(ctx *echo.Context) error {
	var input struct {
		Namespace string                        `json:"namespace"`
		Subject   adminauthorization.SubjectRef `json:"subject"`
		RoleID    adminauthorization.Role       `json:"roleId"`
		Reason    string                        `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) {
		return nil
	}
	binding := adminauthorization.Binding{ID: ctx.Param("bindingID"), Subject: input.Subject, RoleID: input.RoleID}
	api.applyDelegation(ctx, input.Namespace, input.Reason, &binding, "")
	return nil
}

func (api *readAPI) deleteDelegation(ctx *echo.Context) error {
	var input struct {
		Namespace string `json:"namespace"`
		Reason    string `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) {
		return nil
	}
	api.applyDelegation(ctx, input.Namespace, input.Reason, nil, ctx.Param("bindingID"))
	return nil
}

func (api *readAPI) applyDelegation(ctx *echo.Context, namespace, reason string, binding *adminauthorization.Binding, deleteID string) {
	request := ctx.Request()
	namespace = strings.TrimSpace(namespace)
	if !validChangeReason(reason) || !api.requireNamespaceAuthorization(request, namespace, adminauthorization.OperationCreate) {
		writeError(ctx.Response(), http.StatusForbidden, "forbidden", "namespace delegation is not permitted", requestID(request))
		return
	}
	expectedETag, idempotencyKey, ok := policyWriteHeaders(ctx.Response(), request)
	if !ok {
		return
	}
	result, err := api.policy.ApplyDelegation(request.Context(), adminrevision.DelegationRequest{
		Binding: binding, DeleteID: deleteID, Namespace: namespace, ExpectedETag: expectedETag,
		IdempotencyKey: idempotencyKey, Reason: reason, RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(ctx.Response(), http.StatusNotFound, "not_found", "delegation was not found", requestID(request))
		} else {
			writePolicyError(ctx.Response(), request, err)
		}
		return
	}
	if err := api.reloader.Load(request.Context()); err != nil {
		writeError(ctx.Response(), http.StatusServiceUnavailable, "policy_reload_failed", "delegation was stored but is not yet available", requestID(request))
		return
	}
	ctx.Response().Header().Set("ETag", strongETag(result.Active.ETag))
	if result.Replayed {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(ctx.Response(), http.StatusOK, activationDocument(result))
}

func (api *readAPI) requireNamespaceAuthorization(request *http.Request, namespace string, operation adminauthorization.Operation) bool {
	subject, ok := request.Context().Value(subjectContextKey).(adminauthorization.Subject)
	return ok && namespace != "" && api.authorizer.Authorize(request.Context(), subject, adminauthorization.Request{
		Resource: adminauthorization.ResourceNamespacePolicy, Operation: operation, Namespace: namespace,
	}).Allowed
}

func delegationOwnsNamespace(binding adminauthorization.Binding, namespace string) bool {
	return binding.ManagedBy == adminauthorization.ManagedByDelegated && binding.Scope.Type == adminauthorization.ScopeNamespaces &&
		len(binding.Scope.Names) == 1 && binding.Scope.Names[0] == namespace && len(binding.Scope.LabelSelectors) == 0
}

func delegatableRoles(custom []adminauthorization.RoleDefinition) []adminauthorization.RoleDefinition {
	roles := append(adminauthorization.BuiltInRoleDefinitions(), custom...)
	result := make([]adminauthorization.RoleDefinition, 0)
	for _, role := range roles {
		if role.Delegatable {
			result = append(result, role)
		}
	}
	return result
}
