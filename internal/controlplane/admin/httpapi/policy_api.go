package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const (
	maximumPolicyRequestBytes = int64(64 << 10)
	maximumDryRunChecks       = 100
)

type policySpec struct {
	Version     int                             `json:"version"`
	Assignments []adminauthorization.Assignment `json:"assignments"`
}

type policyCheck struct {
	Subject struct {
		ID     string   `json:"id"`
		Groups []string `json:"groups,omitempty"`
	} `json:"subject"`
	Request adminauthorization.Request `json:"request"`
}

func (api *readAPI) currentPolicy(writer http.ResponseWriter, request *http.Request) {
	state, err := api.policy.CurrentPolicy(request.Context())
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/read", "failure")
		writePolicyError(writer, request, err)
		return
	}
	writer.Header().Set("ETag", strongETag(state.Pointer.ETag))
	api.audit(request, subjectFromRequest(request), "admin.policy/read", "success")
	document := map[string]any{
		"active": state.Active, "etag": state.Pointer.ETag, "revision": state.Snapshot.Revision,
		"spec": policySpec{Version: state.Snapshot.Version, Assignments: state.Snapshot.Assignments},
	}
	if state.Active {
		document["createdAt"] = state.Revision.CreatedAt
		document["reason"] = state.Revision.Reason
	}
	writeJSON(writer, http.StatusOK, document)
}

func (api *readAPI) dryRunPolicy(writer http.ResponseWriter, request *http.Request) {
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		return
	}
	_ = idempotencyKey // Dry-run is deterministic and has no state to reserve.
	var input struct {
		Spec   policySpec    `json:"spec"`
		Checks []policyCheck `json:"checks"`
		Reason string        `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		return
	}
	if len(input.Checks) > maximumDryRunChecks || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy dry-run request is invalid", requestID(request))
		return
	}
	state, err := api.policy.CurrentPolicy(request.Context())
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		writePolicyError(writer, request, err)
		return
	}
	if state.Pointer.ETag != expectedETag {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		writeError(writer, http.StatusPreconditionFailed, "etag_mismatch", "management policy changed", requestID(request))
		return
	}
	revision := state.Snapshot.Revision
	if revision == 0 {
		revision = 1
	}
	snapshot := input.Spec.snapshot(revision)
	engine, err := adminauthorization.New(snapshot)
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_policy", "management policy is invalid", requestID(request))
		return
	}
	decisions := make([]map[string]any, 0, len(input.Checks))
	for _, check := range input.Checks {
		decision := engine.DryRun(request.Context(), adminauthorization.Subject{
			ID: check.Subject.ID, Groups: append([]string(nil), check.Subject.Groups...),
		}, check.Request)
		decisions = append(decisions, map[string]any{
			"allowed": decision.Allowed, "reason": decision.Reason, "role": decision.Role,
			"assignmentId": decision.AssignmentID, "scope": decision.Scope, "revision": decision.Revision,
		})
	}
	api.audit(request, subjectFromRequest(request), "admin.policy/dry-run", "success")
	writeJSON(writer, http.StatusOK, map[string]any{
		"valid": true, "publishable": hasPlatformAdministrator(input.Spec.Assignments),
		"baseEtag": expectedETag, "decisions": decisions,
	})
}

func (api *readAPI) createPolicyDraft(writer http.ResponseWriter, request *http.Request) {
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		return
	}
	var input struct {
		Spec   policySpec `json:"spec"`
		Reason string     `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		return
	}
	if !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy draft request is invalid", requestID(request))
		return
	}
	result, err := api.policy.CreatePolicyDraft(request.Context(), adminrevision.PolicyDraftRequest{
		Snapshot: input.Spec.snapshot(0), ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey,
		Reason: input.Reason, RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/create", "failure")
		writePolicyError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v2/admin/policy/changes/"+result.Change.ID)
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, map[bool]int{true: http.StatusOK, false: http.StatusCreated}[result.Replayed], map[string]any{
		"changeId": result.Change.ID, "revision": result.Revision.Revision,
		"baseRevision": result.Change.BaseRevision, "baseEtag": result.Change.BaseETag,
		"status": result.Change.Status, "replayed": result.Replayed,
	})
}

func (api *readAPI) publishPolicy(writer http.ResponseWriter, request *http.Request) {
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		return
	}
	changeID := strings.TrimSpace(request.PathValue("changeID"))
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		return
	}
	if changeID == "" || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy publish request is invalid", requestID(request))
		return
	}
	result, err := api.policy.PublishPolicy(request.Context(), adminrevision.ActivateRequest{
		ChangeID: changeID, ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey,
		Reason: input.Reason, RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/publish", "failure")
		writePolicyError(writer, request, err)
		return
	}
	if err := api.reloader.Load(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "policy_reload_failed", "policy was stored but is not yet available", requestID(request))
		return
	}
	writer.Header().Set("ETag", strongETag(result.Active.ETag))
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, activationDocument(result))
}

func (api *readAPI) rollbackPolicy(writer http.ResponseWriter, request *http.Request) {
	expectedETag, idempotencyKey, ok := policyWriteHeaders(writer, request)
	if !ok {
		api.audit(request, subjectFromRequest(request), "admin.policy/rollback", "failure")
		return
	}
	var input struct {
		TargetRevision uint64 `json:"targetRevision"`
		Reason         string `json:"reason"`
	}
	if !decodePolicyJSON(writer, request, &input) {
		api.audit(request, subjectFromRequest(request), "admin.policy/rollback", "failure")
		return
	}
	if input.TargetRevision == 0 || !validChangeReason(input.Reason) {
		api.audit(request, subjectFromRequest(request), "admin.policy/rollback", "failure")
		writeError(writer, http.StatusBadRequest, "invalid_request", "policy rollback request is invalid", requestID(request))
		return
	}
	result, err := api.policy.RollbackPolicy(request.Context(), adminrevision.RollbackRequest{
		TargetRevision: input.TargetRevision, ExpectedETag: expectedETag, IdempotencyKey: idempotencyKey,
		Reason: input.Reason, RequestID: requestID(request), Actor: policyActor(subjectFromRequest(request)),
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.policy/rollback", "failure")
		writePolicyError(writer, request, err)
		return
	}
	if err := api.reloader.Load(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "policy_reload_failed", "policy was stored but is not yet available", requestID(request))
		return
	}
	writer.Header().Set("ETag", strongETag(result.Active.ETag))
	if result.Replayed {
		writer.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(writer, http.StatusOK, activationDocument(result))
}

func (spec policySpec) snapshot(revision uint64) adminauthorization.Snapshot {
	return adminauthorization.Snapshot{
		Version: spec.Version, Revision: revision,
		Assignments: append([]adminauthorization.Assignment(nil), spec.Assignments...),
	}
}

func activationDocument(result adminrevision.Activation) map[string]any {
	return map[string]any{
		"changeId": result.ChangeID, "revision": result.Active.Revision,
		"etag": result.Active.ETag, "replayed": result.Replayed,
	}
}

func policyActor(subject adminauthorization.Subject) adminrevision.Actor {
	principalID := subject.ID
	if subject.Authentication == adminauthorization.AuthenticationBreakGlass {
		principalID = ""
	}
	return adminrevision.Actor{PrincipalID: principalID, Authentication: subject.Authentication}
}

func policyWriteHeaders(writer http.ResponseWriter, request *http.Request) (uint64, string, bool) {
	etag, err := parseIfMatch(request.Header.Values("If-Match"))
	if err != nil {
		writeError(writer, http.StatusPreconditionRequired, "precondition_required", "a strong If-Match ETag is required", requestID(request))
		return 0, "", false
	}
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeError(writer, http.StatusBadRequest, "invalid_idempotency_key", "a single Idempotency-Key is required", requestID(request))
		return 0, "", false
	}
	key := strings.TrimSpace(keys[0])
	if len(key) < 16 || len(key) > 256 || strings.ContainsAny(key, "\x00\r\n") {
		writeError(writer, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is invalid", requestID(request))
		return 0, "", false
	}
	return etag, key, true
}

func parseIfMatch(values []string) (uint64, error) {
	if len(values) != 1 {
		return 0, errors.New("If-Match is required")
	}
	value := strings.TrimSpace(values[0])
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.Contains(value, ",") {
		return 0, errors.New("If-Match must be a strong integer ETag")
	}
	return strconv.ParseUint(value[1:len(value)-1], 10, 64)
}

func strongETag(value uint64) string { return `"` + strconv.FormatUint(value, 10) + `"` }

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

func hasPlatformAdministrator(assignments []adminauthorization.Assignment) bool {
	for _, assignment := range assignments {
		if assignment.Role == adminauthorization.RolePlatformAdmin &&
			(len(assignment.Subjects) > 0 || len(assignment.Groups) > 0) {
			return true
		}
	}
	return false
}

func writePolicyError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, storage.ErrIdempotencyMismatch):
		writeError(writer, http.StatusConflict, "idempotency_mismatch", "Idempotency-Key was already used for another request", requestID(request))
	case errors.Is(err, adminrevision.ErrConflict), errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusPreconditionFailed, "etag_mismatch", "management policy changed", requestID(request))
	case errors.Is(err, adminrevision.ErrInvalidRequest):
		writeError(writer, http.StatusBadRequest, "invalid_policy", "management policy request is invalid", requestID(request))
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "management policy change was not found", requestID(request))
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "management policy is unavailable", requestID(request))
	}
}
