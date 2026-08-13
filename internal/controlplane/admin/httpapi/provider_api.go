package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

type providerInput struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
	Reason string          `json:"reason,omitempty"`
}

func (api *readAPI) listProviders(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	states, err := api.providers.ListCurrent(request.Context())
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.provider/list", "failure")
		writeProviderError(writer, request, err)
		return nil
	}
	items := make([]map[string]any, 0, len(states))
	for _, state := range states {
		items = append(items, providerDocument(state))
	}
	api.audit(request, subjectFromRequest(request), "admin.provider/list", "success")
	writeJSON(writer, http.StatusOK, map[string]any{"items": items})
	return nil
}

func (api *readAPI) currentProvider(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	state, err := api.providers.Current(request.Context(), request.PathValue("providerID"))
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.provider/read", "failure")
		writeProviderError(writer, request, err)
		return nil
	}
	writer.Header().Set("ETag", strongETag(state.Pointer.ETag))
	api.audit(request, subjectFromRequest(request), "admin.provider/read", "success")
	writeJSON(writer, http.StatusOK, providerDocument(state))
	return nil
}

func (api *readAPI) validateProvider(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	expectedETag, _, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.provider/validate", "failure")
		return nil
	}
	var input providerInput
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.provider/validate", "failure")
		return nil
	}
	if !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.provider/validate", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "Provider validation request is invalid", requestID(request))
		return nil
	}
	if !providerETagMatches(request, api.providers, expectedETag) {
		writeError(writer, http.StatusPreconditionFailed, "etag_mismatch", "Provider changed", requestID(request))
		return nil
	}
	validation, err := api.providers.Validate(request.Context(), providerCandidate(request.PathValue("providerID"), input))
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.provider/validate", "failure")
		writeProviderError(writer, request, err)
		return nil
	}
	api.audit(request, subjectFromRequest(request), "admin.provider/validate", "success")
	writeJSON(writer, http.StatusOK, map[string]any{"valid": true, "baseEtag": expectedETag, "validation": validation})
	return nil
}

func (api *readAPI) createProviderDraft(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.provider/create", "failure")
		return nil
	}
	var input providerInput
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.provider/create", "failure")
		return nil
	}
	if !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.provider/create", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "Provider draft request is invalid", requestID(request))
		return nil
	}
	result, err := api.providers.CreateDraft(request.Context(), adminrevision.ProviderDraftRequest{
		Candidate: providerCandidate(request.PathValue("providerID"), input), ExpectedETag: expectedETag,
		IdempotencyKey: idempotencyKey, Reason: input.Reason, RequestID: requestID(request),
		Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.provider/create", "failure")
		writeProviderError(writer, request, err)
		return nil
	}
	writer.Header().Set("Location", api.handler.pathPrefix+"/providers/"+result.Revision.ProviderID+"/changes/"+result.Change.ID)
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusOK, false: http.StatusCreated}[result.Replayed], map[string]any{
		"providerId": result.Revision.ProviderID, "type": result.Revision.ProviderType,
		"changeId": result.Change.ID, "revision": result.Revision.Revision,
		"baseRevision": result.Change.BaseRevision, "baseEtag": result.Change.BaseETag,
		"status": result.Change.Status, "replayed": result.Replayed,
		"clientSecretConfigured": providerClientSecretConfigured(result.Revision.Config),
	})
	return nil
}

func (api *readAPI) publishProvider(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.provider/publish", "failure")
		return nil
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.provider/publish", "failure")
		return nil
	}
	if !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.provider/publish", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "Provider publish request is invalid", requestID(request))
		return nil
	}
	result, err := api.providers.Publish(request.Context(), adminrevision.ProviderActivateRequest{
		ProviderID: request.PathValue("providerID"), ChangeID: request.PathValue("changeID"),
		ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey, Reason: input.Reason,
		RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.provider/publish", "failure")
		writeProviderError(writer, request, err)
		return nil
	}
	writer.Header().Set("ETag", strongETag(result.Active.ETag))
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, activationDocument(result))
	return nil
}

func (api *readAPI) rollbackProvider(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.provider/rollback", "failure")
		return nil
	}
	var input struct {
		TargetRevision uint64 `json:"targetRevision"`
		Reason         string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.provider/rollback", "failure")
		return nil
	}
	if input.TargetRevision == 0 || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.provider/rollback", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "Provider rollback request is invalid", requestID(request))
		return nil
	}
	result, err := api.providers.Rollback(request.Context(), adminrevision.ProviderRollbackRequest{
		ProviderID: request.PathValue("providerID"), TargetRevision: input.TargetRevision,
		ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey, Reason: input.Reason,
		RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.provider/rollback", "failure")
		writeProviderError(writer, request, err)
		return nil
	}
	writer.Header().Set("ETag", strongETag(result.Active.ETag))
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, activationDocument(result))
	return nil
}

func providerCandidate(id string, input providerInput) adminrevision.ProviderCandidate {
	return adminrevision.ProviderCandidate{ID: id, Type: input.Type, Config: input.Config}
}

func providerETagMatches(request *http.Request, service *adminrevision.ProviderService, expected uint64) bool {
	state, err := service.Current(request.Context(), request.PathValue("providerID"))
	return err == nil && state.Pointer.ETag == expected
}

func providerDocument(state adminrevision.ProviderState) map[string]any {
	document := map[string]any{"active": state.Active, "etag": state.Pointer.ETag}
	if !state.Active {
		return document
	}
	document["providerId"] = state.Revision.ProviderID
	document["type"] = state.Revision.ProviderType
	document["revision"] = state.Revision.Revision
	document["config"] = redactProviderConfig(state.Revision.Config)
	document["clientSecretConfigured"] = providerClientSecretConfigured(state.Revision.Config)
	document["validation"] = state.Revision.Validation
	document["reason"] = state.Revision.Reason
	document["createdAt"] = state.Revision.CreatedAt
	return document
}

func redactProviderConfig(raw json.RawMessage) json.RawMessage {
	var config map[string]any
	if json.Unmarshal(raw, &config) != nil {
		return json.RawMessage(`{}`)
	}
	delete(config, "clientSecret")
	redacted, _ := json.Marshal(config)
	return redacted
}

func providerClientSecretConfigured(raw json.RawMessage) bool {
	var config struct {
		ClientSecret string `json:"clientSecret"`
	}
	return json.Unmarshal(raw, &config) == nil && strings.TrimSpace(config.ClientSecret) != ""
}

func writeProviderError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		writeError(writer, http.StatusConflict, "idempotency_mismatch", "Idempotency-Key was already used for another request", requestID(request))
	case errors.Is(err, adminrevision.ErrConflict), errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusPreconditionFailed, "etag_mismatch", "Provider changed", requestID(request))
	case errors.Is(err, adminrevision.ErrInvalidRequest):
		writeError(writer, http.StatusBadRequest, "invalid_provider", "Provider request is invalid", requestID(request))
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "Provider change was not found", requestID(request))
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "Provider is unavailable", requestID(request))
	}
}
