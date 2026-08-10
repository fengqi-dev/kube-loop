package httpapi

import (
	"errors"
	"net/http"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	adminoperations "github.com/fengqi-dev/kube-loop/internal/controller/admin/operations"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/go-chi/chi/v5"
)

func (api *readAPI) revokePrincipalSessions(writer http.ResponseWriter, request *http.Request) {
	key, ok := operationIdempotencyKey(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.principal/revoke", "failure")
		return
	}
	principalID := strings.TrimSpace(chi.URLParam(request, "principalID"))
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
		PrincipalID:     strings.TrimSpace(chi.URLParam(request, "principalID")),
		DeviceSessionID: strings.TrimSpace(chi.URLParam(request, "deviceSessionID")),
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
		SessionID: strings.TrimSpace(chi.URLParam(request, "sessionID")), ExpectedGeneration: expectedGeneration,
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
		TaskID: strings.TrimSpace(chi.URLParam(request, "taskID")), ExpectedVersion: expectedVersion,
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
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "management resource was not found", requestID(request))
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management operation is unavailable", requestID(request))
	}
}
