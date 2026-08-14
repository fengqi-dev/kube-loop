package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/managementconfig"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

const (
	maximumPolicyRequestBytes = int64(64 << 10)
	maximumDryRunChecks       = 100
)

type policySpec struct {
	Version  int                                 `json:"version"`
	Roles    []adminauthorization.RoleDefinition `json:"roles,omitempty"`
	Bindings []adminauthorization.Binding        `json:"bindings"`
}

type policyCheck struct {
	Subject struct {
		ID       string   `json:"id"`
		Provider string   `json:"provider,omitempty"`
		Groups   []string `json:"groups,omitempty"`
	} `json:"subject"`
	Request adminauthorization.Request `json:"request"`
}

func (api *readAPI) currentPolicy(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	state, err := api.policy.CurrentPolicy(request.Context())
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/read", "failure")
		writePolicyError(writer, request, err)
		return nil
	}
	api.audit(request, subjectFromRequest(request), "admin.policy/read", "success")
	document := map[string]any{
		"active":                state.Active,
		"spec":                  policySpec{Version: state.Snapshot.Version, Roles: state.Snapshot.Roles, Bindings: state.Snapshot.Bindings},
		"availableCapabilities": adminauthorization.AvailableCapabilities(),
		"builtInRoles":          adminauthorization.BuiltInRoleDefinitions(),
	}
	if state.Active {
		document["createdAt"] = state.Config.CreatedAt
		document["reason"] = state.Config.Reason
	}
	writeJSON(writer, http.StatusOK, document)
	return nil
}

func (api *readAPI) dryRunPolicy(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	var input struct {
		Spec   policySpec    `json:"spec"`
		Checks []policyCheck `json:"checks"`
		Reason string        `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		return nil
	}
	if len(input.Checks) > maximumDryRunChecks || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy dry-run request is invalid", requestID(request))
		return nil
	}
	snapshot := input.Spec.snapshot()
	engine, err := adminauthorization.New(snapshot)
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_policy", "management policy is invalid", requestID(request))
		return nil
	}
	decisions := make([]adminauthorization.Decision, 0, len(input.Checks))
	for _, check := range input.Checks {
		decision := engine.DryRun(request.Context(), adminauthorization.Subject{
			ID: check.Subject.ID, Provider: check.Subject.Provider, Groups: append([]string(nil), check.Subject.Groups...),
		}, check.Request)
		decisions = append(decisions, decision)
	}
	api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "success")
	writeJSON(writer, http.StatusOK, map[string]any{
		"valid": true, "publishable": hasPlatformAdministrator(input.Spec.Bindings),
		"decisions": decisions,
	})
	return nil
}

func (api *readAPI) createPolicyDraft(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		return nil
	}
	var input struct {
		Spec   policySpec `json:"spec"`
		Reason string     `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		return nil
	}
	if !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy draft request is invalid", requestID(request))
		return nil
	}
	result, err := api.policy.CreatePolicyDraft(request.Context(), adminconfig.PolicyDraftRequest{
		Snapshot: input.Spec.snapshot(), IdempotencyKey: idempotencyKey,
		Reason: input.Reason, RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		writePolicyError(writer, request, err)
		return nil
	}
	writer.Header().Set("Location", api.handler.pathPrefix+"/authorization/changes/"+result.Change.ID)
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusOK, false: http.StatusCreated}[result.Replayed], map[string]any{
		"changeId": result.Change.ID, "objectId": result.Config.ID,
		"status": result.Change.Status, "replayed": result.Replayed,
	})
	return nil
}

func (api *readAPI) publishPolicy(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		return nil
	}
	changeID := strings.TrimSpace(request.PathValue("changeID"))
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		return nil
	}
	if changeID == "" || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy publish request is invalid", requestID(request))
		return nil
	}
	result, err := api.policy.PublishPolicy(request.Context(), adminconfig.ActivateRequest{
		ChangeID: changeID, IdempotencyKey: idempotencyKey,
		Reason: input.Reason, RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		writePolicyError(writer, request, err)
		return nil
	}
	if err := api.reloader.Load(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "policy_reload_failed", "policy was stored but is not yet available", requestID(request))
		return nil
	}
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, activationDocument(result))
	return nil
}

func (spec policySpec) snapshot() adminauthorization.Snapshot {
	return adminauthorization.Snapshot{
		Version:  spec.Version,
		Roles:    append([]adminauthorization.RoleDefinition(nil), spec.Roles...),
		Bindings: append([]adminauthorization.Binding(nil), spec.Bindings...),
	}
}

func activationDocument(result adminconfig.Activation) map[string]any {
	return map[string]any{
		"changeId": result.ChangeID, "objectId": result.Active.ObjectID, "replayed": result.Replayed,
	}
}

func policyActor(subject adminauthorization.Subject) adminconfig.Actor {
	principalID := subject.ID
	if subject.Authentication == adminauthorization.AuthenticationBreakGlass {
		principalID = ""
	}
	return adminconfig.Actor{PrincipalID: principalID, Authentication: subject.Authentication}
}

func policyWriteHeaders(writer http.ResponseWriter, request *http.Request) (string, bool) {
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeError(writer, http.StatusBadRequest, "invalid_idempotency_key", "a single Idempotency-Key is required", requestID(request))
		return "", false
	}
	key := strings.TrimSpace(keys[0])
	if len(key) < 16 || len(key) > 256 || strings.ContainsAny(key, "\x00\r\n") {
		writeError(writer, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is invalid", requestID(request))
		return "", false
	}
	return key, true
}

func decodePolicyJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_content_type", "application/json is required", requestID(request))
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumPolicyRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid", requestID(request))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid", requestID(request))
		return false
	}
	return true
}

func validChangeReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 8 && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}

func hasPlatformAdministrator(bindings []adminauthorization.Binding) bool {
	for _, binding := range bindings {
		if binding.RoleID == adminauthorization.RolePlatformAdmin &&
			binding.Subject.Type == adminauthorization.SubjectPrincipal &&
			binding.Scope.Type == adminauthorization.ScopePlatform {
			return true
		}
	}
	return false
}

func writePolicyError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		writeError(writer, http.StatusConflict, "idempotency_mismatch", "Idempotency-Key was already used for another request", requestID(request))
	case errors.Is(err, adminconfig.ErrConflict), errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusConflict, "configuration_conflict", "management policy change conflicts with current state", requestID(request))
	case errors.Is(err, adminconfig.ErrInvalidRequest):
		writeError(writer, http.StatusBadRequest, "invalid_policy", "management policy request is invalid", requestID(request))
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "management policy change was not found", requestID(request))
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management policy is unavailable", requestID(request))
	}
}
