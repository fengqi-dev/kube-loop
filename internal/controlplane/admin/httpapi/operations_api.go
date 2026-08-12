package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/operations"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (api *readAPI) revokePrincipalSessions(writer http.ResponseWriter, request *http.Request) {
	key, ok := operationIdempotencyKey(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.principal/revoke", "failure")
		return
	}
	principalID := strings.TrimSpace(request.PathValue("principalID"))
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.principal/revoke", "failure")
		return
	}
	result, err := api.operations.RevokePrincipal(request.Context(), adminoperations.RevokePrincipalRequest{
		Request: adminoperations.Request{
			Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
			Reason: input.Reason, RequestID: requestID(request),
		},
		PrincipalID: principalID,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.principal/revoke", "failure")
		writeOperationError(writer, request, err)
		return
	}
	writeOperationResult(writer, result.Replayed, result)
}

func (api *readAPI) revokeDeviceSession(writer http.ResponseWriter, request *http.Request) {
	key, ok := operationIdempotencyKey(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.device-session/revoke", "failure")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.device-session/revoke", "failure")
		return
	}
	result, err := api.operations.RevokeDeviceSession(request.Context(), adminoperations.RevokeDeviceSessionRequest{
		Request: adminoperations.Request{
			Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
			Reason: input.Reason, RequestID: requestID(request),
		},
		PrincipalID:     strings.TrimSpace(request.PathValue("principalID")),
		DeviceSessionID: strings.TrimSpace(request.PathValue("deviceSessionID")),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.device-session/revoke", "failure")
		writeOperationError(writer, request, err)
		return
	}
	writeOperationResult(writer, result.Replayed, result)
}

func (api *readAPI) stopSession(writer http.ResponseWriter, request *http.Request) {
	expectedGeneration, key, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.session/stop", "failure")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.session/stop", "failure")
		return
	}
	result, err := api.operations.StopSession(request.Context(), adminoperations.StopSessionRequest{
		Request: adminoperations.Request{
			Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
			Reason: input.Reason, RequestID: requestID(request),
		},
		SessionID: strings.TrimSpace(request.PathValue("sessionID")), ExpectedGeneration: expectedGeneration,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.session/stop", "failure")
		writeOperationError(writer, request, err)
		return
	}
	writer.Header().Set("ETag", strongETag(result.Generation))
	if !result.RuntimeConverged {
		writer.Header().Set("Retry-After", "1")
	}
	writeOperationResult(writer, result.Replayed, result)
}

func (api *readAPI) stopTask(writer http.ResponseWriter, request *http.Request) {
	expectedVersion, key, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.task/stop", "failure")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.task/stop", "failure")
		return
	}
	result, err := api.operations.StopTask(request.Context(), adminoperations.StopTaskRequest{
		Request: adminoperations.Request{
			Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
			Reason: input.Reason, RequestID: requestID(request),
		},
		TaskID: strings.TrimSpace(request.PathValue("taskID")), ExpectedVersion: expectedVersion,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.task/stop", "failure")
		writeOperationError(writer, request, err)
		return
	}
	writer.Header().Set("ETag", strongETag(result.Version))
	if result.PendingConvergence {
		writer.Header().Set("Retry-After", "1")
	}
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[result.PendingConvergence], result)
}

func (api *readAPI) drainRelay(writer http.ResponseWriter, request *http.Request) {
	api.changeRelayState(writer, request, true)
}

func (api *readAPI) recoverRelay(writer http.ResponseWriter, request *http.Request) {
	api.changeRelayState(writer, request, false)
}

func (api *readAPI) changeRelayState(writer http.ResponseWriter, request *http.Request, drain bool) {
	expectedVersion, key, ok := policyWriteHeaders(writer, request)
	action := "admin.relay/recover"
	if drain {
		action = "admin.relay/drain"
	}
	if !ok {
		api.audit(request, subjectFromRequest(request), action, "failure")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), action, "failure")
		return
	}
	operationRequest := adminoperations.ChangeRelayStateRequest{
		Request: adminoperations.Request{
			Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
			Reason: input.Reason, RequestID: requestID(request),
		},
		RelayID: strings.TrimSpace(request.PathValue("relayID")), ExpectedVersion: expectedVersion,
	}
	var result adminoperations.RelayStateResult
	var err error
	if drain {
		result, err = api.operations.DrainRelay(request.Context(), operationRequest)
	} else {
		result, err = api.operations.RecoverRelay(request.Context(), operationRequest)
	}
	if err != nil {
		api.audit(request, subjectFromRequest(request), action, "failure")
		writeOperationError(writer, request, err)
		return
	}
	writer.Header().Set("ETag", strongETag(result.Version))
	if result.PendingConvergence {
		writer.Header().Set("Retry-After", "1")
	}
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[result.PendingConvergence], result)
}

func (api *readAPI) triggerRecovery(writer http.ResponseWriter, request *http.Request) {
	key, ok := operationIdempotencyKey(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.recovery/run", "failure")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.recovery/run", "failure")
		return
	}
	result, err := api.operations.TriggerRecovery(request.Context(), adminoperations.TriggerRecoveryRequest{Request: adminoperations.Request{
		Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
		Reason: input.Reason, RequestID: requestID(request),
	}})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.recovery/run", "failure")
		writeOperationError(writer, request, err)
		return
	}
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	if result.PendingConvergence {
		writer.Header().Set("Retry-After", "1")
	}
	writeJSON(writer, map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[result.PendingConvergence], result)
}

func (api *readAPI) createAuditExport(writer http.ResponseWriter, request *http.Request) {
	key, ok := operationIdempotencyKey(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.audit/export", "failure")
		return
	}
	var input struct {
		PrincipalID string `json:"principalId"`
		Action      string `json:"action"`
		After       string `json:"after"`
		Before      string `json:"before"`
		Limit       int    `json:"limit"`
		Reason      string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.audit/export", "failure")
		return
	}
	after, afterErr := optionalTime(input.After)
	before, beforeErr := optionalTime(input.Before)
	if afterErr != nil || beforeErr != nil {
		api.audit(request, subjectFromRequest(request), "admin.audit/export", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "audit export filter is invalid", requestID(request))
		return
	}
	result, err := api.operations.CreateAuditExport(request.Context(), adminoperations.AuditExportRequest{
		Request: adminoperations.Request{
			Actor: operationActor(subjectFromRequest(request)), IdempotencyKey: key,
			Reason: input.Reason, RequestID: requestID(request),
		},
		PrincipalID: input.PrincipalID, Action: input.Action, After: after, Before: before, Limit: input.Limit,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.audit/export", "failure")
		writeOperationError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/admin/audit/exports/"+result.JobID)
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func (api *readAPI) getAuditExport(writer http.ResponseWriter, request *http.Request) {
	result, data, err := api.operations.GetAuditExport(
		request.Context(), operationActor(subjectFromRequest(request)), strings.TrimSpace(request.PathValue("jobID")),
	)
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.audit-export/read", "failure")
		writeOperationError(writer, request, err)
		return
	}
	api.audit(request, subjectFromRequest(request), "admin.audit-export/read", "success")
	if result.State != "succeeded" {
		pending := result.State == "pending" || result.State == "running"
		if pending {
			writer.Header().Set("Retry-After", "1")
		}
		writeJSON(writer, map[bool]int{true: http.StatusAccepted, false: http.StatusOK}[pending], result)
		return
	}
	writer.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	writer.Header().Set("Content-Disposition", `attachment; filename="kubeloop-audit-`+result.JobID+`.ndjson"`)
	writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(data))
}

func operationActor(subject adminauthorization.Subject) adminoperations.Actor {
	principalID := subject.ID
	if subject.Authentication == adminauthorization.AuthenticationBreakGlass {
		principalID = ""
	}
	return adminoperations.Actor{PrincipalID: principalID, Authentication: subject.Authentication}
}

func operationIdempotencyKey(writer http.ResponseWriter, request *http.Request) (string, bool) {
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

func writeOperationResult(writer http.ResponseWriter, replayed bool, result any) {
	if replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, result)
}

func writeOperationError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		writeError(writer, http.StatusConflict, "idempotency_mismatch", "Idempotency-Key was already used for another request", requestID(request))
	case errors.Is(err, adminoperations.ErrConflict), errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusPreconditionFailed, "etag_mismatch", "management resource changed", requestID(request))
	case errors.Is(err, adminoperations.ErrInvalidRequest):
		writeError(writer, http.StatusBadRequest, "invalid_request", "management operation request is invalid", requestID(request))
	case errors.Is(err, adminoperations.ErrUnavailable):
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management operation runtime is unavailable", requestID(request))
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "management resource was not found", requestID(request))
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management operation is unavailable", requestID(request))
	}
}
