// Package operations implements durable, audited Management Plane actions.
// Database state is committed before best-effort runtime convergence begins.
package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest = errors.New("management operation request is invalid")
	ErrConflict       = errors.New("management operation precondition failed")
	ErrUnavailable    = errors.New("management operation runtime is unavailable")
)

const (
	idempotencyLifetime   = 24 * time.Hour
	runtimeTimeout        = 5 * time.Second
	exportWorkerInterval  = 500 * time.Millisecond
	maximumExportRows     = 1000
	maximumExportBytes    = 4 << 20
	exportClaimStaleAfter = 30 * time.Second
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionRuntime interface {
	Disconnect(context.Context, string) error
}

type RelayRuntime interface {
	Snapshot() []relayregistry.RelayStatus
	RestoreDesiredState(string, relaycontrol.State) error
}

type RecoveryRunner interface {
	RunOnce(context.Context) (map[string]int, error)
}

type RecoveryRunnerFunc func(context.Context) (map[string]int, error)

func (function RecoveryRunnerFunc) RunOnce(ctx context.Context) (map[string]int, error) {
	return function(ctx)
}

type Actor struct {
	PrincipalID    string
	Authentication adminauthorization.AuthenticationType
}

type Request struct {
	Actor          Actor
	IdempotencyKey string
	Reason         string
	RequestID      string
}

type RevokeOAuthGrantRequest struct {
	Request
	PrincipalID     string
	AuthorizationID string
}

type RevokePrincipalRequest struct {
	Request
	PrincipalID string
}

type StopSessionRequest struct {
	Request
	SessionID          string
	ExpectedGeneration uint64
}

type StopTaskRequest struct {
	Request
	TaskID          string
	ExpectedVersion uint64
}

type ChangeRelayStateRequest struct {
	Request
	RelayID         string
	ExpectedVersion uint64
}

type TriggerRecoveryRequest struct{ Request }

type AuditExportRequest struct {
	Request
	PrincipalID string
	Action      string
	After       time.Time
	Before      time.Time
	Limit       int
}

type RevocationResult struct {
	PrincipalID     string    `json:"principalId"`
	AuthorizationID string    `json:"authorizationId,omitempty"`
	RevokedCount    int64     `json:"revokedCount"`
	RevokedAt       time.Time `json:"revokedAt"`
	Replayed        bool      `json:"replayed"`
}

type StopSessionResult struct {
	SessionID        string `json:"sessionId"`
	State            string `json:"state"`
	Generation       uint64 `json:"generation"`
	RuntimeConverged bool   `json:"runtimeConverged"`
	Replayed         bool   `json:"replayed"`
}

type StopTaskResult struct {
	TaskID             string    `json:"taskId"`
	State              string    `json:"state"`
	Version            uint64    `json:"version"`
	UpdatedAt          time.Time `json:"updatedAt"`
	PendingConvergence bool      `json:"pendingConvergence"`
	Replayed           bool      `json:"replayed"`
}

type RelayStateResult struct {
	RelayID            string `json:"relayId"`
	DesiredState       string `json:"desiredState"`
	Version            uint64 `json:"version"`
	PendingConvergence bool   `json:"pendingConvergence"`
	Replayed           bool   `json:"replayed"`
}

type RecoveryResult struct {
	RequestedAt        time.Time      `json:"requestedAt"`
	RecoveredByType    map[string]int `json:"recoveredByType"`
	PendingConvergence bool           `json:"pendingConvergence"`
	Replayed           bool           `json:"replayed"`
}

type AuditExportResult struct {
	JobID     string    `json:"jobId"`
	State     string    `json:"state"`
	ErrorCode string    `json:"errorCode,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Replayed  bool      `json:"replayed"`
}

type Service struct {
	store    Store
	runtime  SessionRuntime
	relays   RelayRuntime
	recovery RecoveryRunner
	now      func() time.Time
	newID    func() string
}

func New(store Store, runtime SessionRuntime, relayRuntimes ...RelayRuntime) (*Service, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("management operation storage and Session runtime are required")
	}
	var relays RelayRuntime
	if len(relayRuntimes) > 1 {
		return nil, errors.New("only one management Relay runtime may be configured")
	}
	if len(relayRuntimes) == 1 {
		relays = relayRuntimes[0]
	}
	return &Service{store: store, runtime: runtime, relays: relays, now: time.Now, newID: uuid.NewString}, nil
}

func (service *Service) RelayAvailable() bool { return service != nil && service.relays != nil }

func (service *Service) ConfigureRecovery(runner RecoveryRunner) error {
	if service == nil || runner == nil {
		return errors.New("management recovery runner is required")
	}
	if service.recovery != nil {
		return errors.New("management recovery runner is already configured")
	}
	service.recovery = runner
	return nil
}

func (service *Service) RecoveryAvailable() bool { return service != nil && service.recovery != nil }

func (service *Service) RevokeOAuthGrant(ctx context.Context, request RevokeOAuthGrantRequest) (RevocationResult, error) {
	common, err := normalizeRequest(request.Request, "admin.oauth-grant.revoke")
	if err != nil || !validUUID(request.PrincipalID) || !validUUID(request.AuthorizationID) {
		return RevocationResult{}, ErrInvalidRequest
	}
	requestHash := requestDigest(struct {
		PrincipalID     string `json:"principalId"`
		AuthorizationID string `json:"authorizationId"`
		Reason          string `json:"reason"`
	}{request.PrincipalID, request.AuthorizationID, common.reason})
	result := RevocationResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		principalID, _, getErr := repositories.OAuthSessions().RequestOwner(ctx, request.AuthorizationID)
		if getErr != nil {
			return getErr
		}
		if principalID != request.PrincipalID {
			return storage.ErrNotFound
		}
		revokedAt := service.now().UTC()
		active, activeErr := repositories.OAuthSessions().RequestActive(ctx, request.AuthorizationID, revokedAt)
		if activeErr != nil {
			return activeErr
		}
		count := int64(0)
		if active {
			count = 1
		}
		if revokeErr := repositories.OAuthSessions().RevokeRequest(ctx, request.AuthorizationID, revokedAt); revokeErr != nil {
			return revokeErr
		}
		result = RevocationResult{
			PrincipalID: principalID, AuthorizationID: request.AuthorizationID,
			RevokedCount: count, RevokedAt: revokedAt,
		}
		return service.persistSuccess(ctx, repositories, common, requestHash, "oauth-grant", request.AuthorizationID,
			"admin.oauth-grant.revoke", map[string]any{"targetPrincipalId": principalID}, result)
	})
	if err != nil {
		return RevocationResult{}, mapError(err)
	}
	return result, nil
}

func (service *Service) RevokePrincipal(ctx context.Context, request RevokePrincipalRequest) (RevocationResult, error) {
	common, err := normalizeRequest(request.Request, "admin.principal.revoke")
	if err != nil || !validUUID(request.PrincipalID) {
		return RevocationResult{}, ErrInvalidRequest
	}
	requestHash := requestDigest(struct {
		PrincipalID string `json:"principalId"`
		Reason      string `json:"reason"`
	}{request.PrincipalID, common.reason})
	result := RevocationResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		if _, getErr := repositories.Principals().GetByID(ctx, request.PrincipalID); getErr != nil {
			return getErr
		}
		revokedAt := service.now().UTC()
		count, revokeErr := repositories.OAuthSessions().RevokePrincipal(ctx, request.PrincipalID, revokedAt)
		if revokeErr != nil {
			return revokeErr
		}
		result = RevocationResult{PrincipalID: request.PrincipalID, RevokedCount: count, RevokedAt: revokedAt}
		return service.persistSuccess(ctx, repositories, common, requestHash, "principal", request.PrincipalID,
			"admin.principal.revoke", map[string]any{"revokedOAuthGrantCount": count}, result)
	})
	if err != nil {
		return RevocationResult{}, mapError(err)
	}
	return result, nil
}

func (service *Service) StopSession(ctx context.Context, request StopSessionRequest) (StopSessionResult, error) {
	common, err := normalizeRequest(request.Request, "admin.session.stop")
	if err != nil || !validUUID(request.SessionID) || request.ExpectedGeneration == 0 {
		return StopSessionResult{}, ErrInvalidRequest
	}
	requestHash := requestDigest(struct {
		SessionID          string `json:"sessionId"`
		ExpectedGeneration uint64 `json:"expectedGeneration"`
		Reason             string `json:"reason"`
	}{request.SessionID, request.ExpectedGeneration, common.reason})
	result := StopSessionResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		session, getErr := repositories.Sessions().GetByID(ctx, request.SessionID)
		if getErr != nil {
			return getErr
		}
		if session.Generation != request.ExpectedGeneration {
			return storage.ErrConflict
		}
		nextGeneration := session.Generation
		if session.State != "stopped" {
			if updateErr := repositories.Sessions().UpdateState(ctx, session.ID, session.Generation, "stopped", service.now().UTC()); updateErr != nil {
				return updateErr
			}
			nextGeneration++
		}
		result = StopSessionResult{SessionID: session.ID, State: "stopped", Generation: nextGeneration}
		return service.persistSuccess(ctx, repositories, common, requestHash, "session", session.ID,
			"admin.session.stop", map[string]any{
				"oldState": session.State, "oldGeneration": session.Generation, "newGeneration": nextGeneration,
			}, result)
	})
	if err != nil {
		return StopSessionResult{}, mapError(err)
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimeTimeout)
	err = service.runtime.Disconnect(cleanupContext, request.SessionID)
	cancel()
	result.RuntimeConverged = err == nil
	return result, nil
}

func (service *Service) StopTask(ctx context.Context, request StopTaskRequest) (StopTaskResult, error) {
	common, err := normalizeRequest(request.Request, "admin.task.stop")
	if err != nil || !validUUID(request.TaskID) || request.ExpectedVersion == 0 {
		return StopTaskResult{}, ErrInvalidRequest
	}
	requestHash := requestDigest(struct {
		TaskID          string `json:"taskId"`
		ExpectedVersion uint64 `json:"expectedVersion"`
		Reason          string `json:"reason"`
	}{request.TaskID, request.ExpectedVersion, common.reason})
	result := StopTaskResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		task, getErr := repositories.Tasks().GetByID(ctx, request.TaskID)
		if getErr != nil {
			return getErr
		}
		if taskVersion(task.UpdatedAt) != request.ExpectedVersion {
			return storage.ErrConflict
		}
		next, updatedAt := task.State, task.UpdatedAt
		switch task.State {
		case remotetask.Pending:
			next = remotetask.Stopped
		case remotetask.Starting, remotetask.Running, remotetask.Recovering:
			next = remotetask.Stopping
		case remotetask.Stopping, remotetask.Stopped, remotetask.Failed:
		default:
			return storage.ErrConflict
		}
		if next != task.State {
			updatedAt = service.now().UTC()
			stopResult := json.RawMessage(`{"stopRequested":true,"source":"management"}`)
			if updateErr := repositories.Tasks().UpdateState(ctx, task.ID, task.State, next, stopResult, updatedAt); updateErr != nil {
				return updateErr
			}
		}
		result = StopTaskResult{
			TaskID: task.ID, State: string(next), Version: taskVersion(updatedAt), UpdatedAt: updatedAt,
			PendingConvergence: next == remotetask.Stopping,
		}
		return service.persistSuccess(ctx, repositories, common, requestHash, "task", task.ID,
			"admin.task.stop", map[string]any{
				"taskType": task.Type, "oldState": task.State, "newState": next,
				"sessionId": task.SessionID, "oldVersion": request.ExpectedVersion, "newVersion": result.Version,
			}, result)
	})
	if err != nil {
		return StopTaskResult{}, mapError(err)
	}
	return result, nil
}

func (service *Service) DrainRelay(ctx context.Context, request ChangeRelayStateRequest) (RelayStateResult, error) {
	return service.changeRelayState(ctx, request, relaycontrol.StateDraining, "admin.relay.drain")
}

func (service *Service) RecoverRelay(ctx context.Context, request ChangeRelayStateRequest) (RelayStateResult, error) {
	return service.changeRelayState(ctx, request, relaycontrol.StateReady, "admin.relay.recover")
}

func (service *Service) changeRelayState(
	ctx context.Context,
	request ChangeRelayStateRequest,
	desired relaycontrol.State,
	action string,
) (RelayStateResult, error) {
	if service.relays == nil {
		return RelayStateResult{}, ErrUnavailable
	}
	common, err := normalizeRequest(request.Request, action)
	request.RelayID = strings.TrimSpace(request.RelayID)
	if err != nil || request.RelayID == "" || len(request.RelayID) > 256 || strings.ContainsAny(request.RelayID, "\x00\r\n/\\") {
		return RelayStateResult{}, ErrInvalidRequest
	}
	known := false
	for _, status := range service.relays.Snapshot() {
		if status.RelayID == request.RelayID {
			known = true
			break
		}
	}
	if !known {
		if _, lookupErr := service.store.RelayDesiredStates().Get(ctx, request.RelayID); lookupErr != nil {
			return RelayStateResult{}, lookupErr
		}
	}
	requestHash := requestDigest(struct {
		RelayID         string `json:"relayId"`
		DesiredState    string `json:"desiredState"`
		ExpectedVersion uint64 `json:"expectedVersion"`
		Reason          string `json:"reason"`
	}{request.RelayID, string(desired), request.ExpectedVersion, common.reason})
	result := RelayStateResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		current, getErr := repositories.RelayDesiredStates().Get(ctx, request.RelayID)
		if errors.Is(getErr, storage.ErrNotFound) {
			if request.ExpectedVersion != 0 {
				return storage.ErrConflict
			}
		} else if getErr != nil {
			return getErr
		} else if current.Version != request.ExpectedVersion {
			return storage.ErrConflict
		}
		next, swapErr := repositories.RelayDesiredStates().CompareAndSwap(
			ctx, request.RelayID, string(desired), request.ExpectedVersion,
			common.actorID, common.authenticationType, common.reason, service.now().UTC(),
		)
		if swapErr != nil {
			return swapErr
		}
		result = RelayStateResult{
			RelayID: next.RelayID, DesiredState: next.DesiredState, Version: next.Version, PendingConvergence: true,
		}
		return service.persistSuccess(ctx, repositories, common, requestHash, "relay", next.RelayID, action,
			map[string]any{"oldVersion": request.ExpectedVersion, "newVersion": next.Version, "desiredState": next.DesiredState}, result)
	})
	if err != nil {
		return RelayStateResult{}, mapError(err)
	}
	if err := service.relays.RestoreDesiredState(request.RelayID, desired); err != nil {
		return result, nil
	}
	for _, status := range service.relays.Snapshot() {
		if status.RelayID == request.RelayID {
			result.PendingConvergence = status.DesiredState != desired || status.State != desired
			break
		}
	}
	return result, nil
}

func (service *Service) TriggerRecovery(ctx context.Context, request TriggerRecoveryRequest) (RecoveryResult, error) {
	if service.recovery == nil {
		return RecoveryResult{}, ErrUnavailable
	}
	common, err := normalizeRequest(request.Request, "admin.recovery.run")
	if err != nil {
		return RecoveryResult{}, err
	}
	requestHash := requestDigest(struct {
		Reason string `json:"reason"`
	}{common.reason})
	result := RecoveryResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		result = RecoveryResult{
			RequestedAt: service.now().UTC(), RecoveredByType: map[string]int{}, PendingConvergence: true,
		}
		return service.persistSuccess(ctx, repositories, common, requestHash, "recovery", "controlPlane",
			"admin.recovery.run", map[string]any{"scope": "all-stale-owned-tasks"}, result)
	})
	if err != nil {
		return RecoveryResult{}, mapError(err)
	}
	runContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimeTimeout)
	counts, runErr := service.recovery.RunOnce(runContext)
	cancel()
	if counts == nil {
		counts = map[string]int{}
	}
	result.RecoveredByType = counts
	result.PendingConvergence = runErr != nil
	return result, nil
}

func (service *Service) CreateAuditExport(ctx context.Context, request AuditExportRequest) (AuditExportResult, error) {
	common, err := normalizeRequest(request.Request, "admin.audit.export")
	request.PrincipalID, request.Action = strings.TrimSpace(request.PrincipalID), strings.TrimSpace(request.Action)
	if request.Limit == 0 {
		request.Limit = maximumExportRows
	}
	if err != nil || request.Limit < 1 || request.Limit > maximumExportRows ||
		(request.PrincipalID != "" && !validUUID(request.PrincipalID)) || len(request.Action) > 256 ||
		strings.ContainsAny(request.Action, "\x00\r\n") ||
		(!request.After.IsZero() && !request.Before.IsZero() && !request.Before.After(request.After)) {
		return AuditExportResult{}, ErrInvalidRequest
	}
	filter := storage.AuditFilter{
		PrincipalID: request.PrincipalID, Action: request.Action,
		After: request.After.UTC(), Before: request.Before.UTC(), Limit: request.Limit,
	}
	filterJSON, _ := json.Marshal(struct {
		PrincipalID string    `json:"principalId,omitempty"`
		Action      string    `json:"action,omitempty"`
		After       time.Time `json:"after"`
		Before      time.Time `json:"before"`
		Limit       int       `json:"limit"`
	}{filter.PrincipalID, filter.Action, filter.After, filter.Before, filter.Limit})
	requestHash := requestDigest(struct {
		Filter json.RawMessage `json:"filter"`
		Reason string          `json:"reason"`
	}{filterJSON, common.reason})
	now, jobID := service.now().UTC(), service.newID()
	result := AuditExportResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		job := storage.AuditExportJob{
			ID: jobID, State: "pending", Filter: filterJSON, RequestedBy: common.actorID,
			RequestedAuthenticationType: common.authenticationType, Reason: common.reason,
			CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(idempotencyLifetime),
		}
		if createErr := repositories.AuditExportJobs().Create(ctx, job); createErr != nil {
			return createErr
		}
		result = auditExportResult(job)
		return service.persistSuccess(ctx, repositories, common, requestHash, "audit-export", job.ID,
			"admin.audit.export", map[string]any{"limit": filter.Limit}, result)
	})
	if err != nil {
		return AuditExportResult{}, mapError(err)
	}
	return result, nil
}

func (service *Service) GetAuditExport(ctx context.Context, actor Actor, jobID string) (AuditExportResult, string, error) {
	common, err := normalizeRequest(Request{
		Actor: actor, IdempotencyKey: "read-only-placeholder", Reason: "read audit export", RequestID: "read",
	}, "admin.audit.export.read")
	if err != nil || !validUUID(jobID) {
		return AuditExportResult{}, "", ErrInvalidRequest
	}
	job, err := service.store.AuditExportJobs().GetByID(ctx, jobID)
	if err != nil {
		return AuditExportResult{}, "", err
	}
	if job.RequestedBy != common.actorID || job.RequestedAuthenticationType != common.authenticationType {
		return AuditExportResult{}, "", storage.ErrNotFound
	}
	return auditExportResult(job), job.Result, nil
}

func (service *Service) Run(ctx context.Context) {
	service.runAuditExports(ctx)
	ticker := time.NewTicker(exportWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			service.runAuditExports(ctx)
		}
	}
}

func (service *Service) runAuditExports(ctx context.Context) {
	staleBefore := service.now().UTC().Add(-exportClaimStaleAfter)
	jobs, err := service.store.AuditExportJobs().ListRunnable(ctx, staleBefore, 10)
	if err != nil {
		return
	}
	for _, job := range jobs {
		now := service.now().UTC()
		if err := service.store.AuditExportJobs().Claim(ctx, job.ID, job.UpdatedAt, staleBefore, now); err != nil {
			continue
		}
		data, errorCode := service.buildAuditExport(ctx, job.Filter)
		state := "succeeded"
		if errorCode != "" {
			state, data = "failed", ""
		}
		_ = service.store.AuditExportJobs().Complete(ctx, job.ID, state, data, errorCode, service.now().UTC())
	}
}

func (service *Service) buildAuditExport(ctx context.Context, raw json.RawMessage) (string, string) {
	var filter struct {
		PrincipalID string    `json:"principalId"`
		Action      string    `json:"action"`
		After       time.Time `json:"after"`
		Before      time.Time `json:"before"`
		Limit       int       `json:"limit"`
	}
	if json.Unmarshal(raw, &filter) != nil || filter.Limit < 1 || filter.Limit > maximumExportRows {
		return "", "invalid_filter"
	}
	events, err := service.store.Audit().List(ctx, storage.AuditFilter{
		PrincipalID: filter.PrincipalID, Action: filter.Action, After: filter.After, Before: filter.Before, Limit: filter.Limit,
	})
	if err != nil {
		return "", "storage_unavailable"
	}
	var output bytes.Buffer
	for _, event := range events {
		line, marshalErr := json.Marshal(struct {
			ID           string          `json:"id"`
			PrincipalID  string          `json:"principalId,omitempty"`
			Action       string          `json:"action"`
			ResourceType string          `json:"resourceType,omitempty"`
			ResourceID   string          `json:"resourceId,omitempty"`
			Outcome      string          `json:"outcome"`
			RequestID    string          `json:"requestId"`
			Metadata     json.RawMessage `json:"metadata,omitempty"`
			CreatedAt    time.Time       `json:"createdAt"`
		}{event.ID, event.PrincipalID, event.Action, event.ResourceType, event.ResourceID, event.Outcome, event.RequestID, event.Metadata, event.CreatedAt})
		if marshalErr != nil || output.Len()+len(line)+1 > maximumExportBytes {
			return "", "export_too_large"
		}
		output.Write(line)
		output.WriteByte('\n')
	}
	if output.Len() == 0 {
		output.WriteByte('\n')
	}
	return output.String(), ""
}

func auditExportResult(job storage.AuditExportJob) AuditExportResult {
	return AuditExportResult{
		JobID: job.ID, State: job.State, ErrorCode: job.ErrorCode,
		CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, ExpiresAt: job.ExpiresAt,
	}
}

type normalizedRequest struct {
	actorID, principalID, authenticationType string
	scope, keyHash, reason, requestID        string
}

func normalizeRequest(request Request, action string) (normalizedRequest, error) {
	request.Reason = strings.TrimSpace(request.Reason)
	request.RequestID = strings.TrimSpace(request.RequestID)
	if len(request.Reason) < 8 || len(request.Reason) > 512 || strings.ContainsAny(request.Reason, "\x00\r\n") || request.RequestID == "" {
		return normalizedRequest{}, ErrInvalidRequest
	}
	actorID, principalID := strings.TrimSpace(request.Actor.PrincipalID), strings.TrimSpace(request.Actor.PrincipalID)
	authenticationType := string(request.Actor.Authentication)
	switch request.Actor.Authentication {
	case adminauthorization.AuthenticationNormal, adminauthorization.AuthenticationBootstrap:
		if !validUUID(actorID) {
			return normalizedRequest{}, ErrInvalidRequest
		}
	case adminauthorization.AuthenticationBreakGlass:
		if actorID != "" {
			return normalizedRequest{}, ErrInvalidRequest
		}
		actorID, principalID = storage.ManagementActorBreakGlass, ""
	default:
		return normalizedRequest{}, ErrInvalidRequest
	}
	key := strings.TrimSpace(request.IdempotencyKey)
	if len(key) < 16 || len(key) > 256 || strings.ContainsAny(key, "\x00\r\n") {
		return normalizedRequest{}, ErrInvalidRequest
	}
	digest := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(digest[:])
	return normalizedRequest{
		actorID: actorID, principalID: principalID, authenticationType: authenticationType,
		scope:   fmt.Sprintf("admin-operation:%s:%s:%s", authenticationType, actorID, action),
		keyHash: keyHash, reason: request.Reason, requestID: request.RequestID,
	}, nil
}

func replay(repositories storage.Repositories, ctx context.Context, scope, keyHash, requestHash string, destination any) (bool, error) {
	record, err := repositories.Idempotency().Get(ctx, scope, keyHash)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.RequestHash != requestHash {
		return false, storage.ErrIdempotencyMismatch
	}
	if len(record.Response) == 0 || json.Unmarshal(record.Response, destination) != nil {
		return false, storage.ErrConflict
	}
	return true, nil
}

func (service *Service) persistSuccess(
	ctx context.Context,
	repositories storage.Repositories,
	request normalizedRequest,
	requestHash, resourceType, resourceID, action string,
	metadata map[string]any,
	response any,
) error {
	now := service.now().UTC()
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	record := storage.IdempotencyRecord{
		Scope: request.scope, Key: request.keyHash, RequestHash: requestHash,
		ResourceType: resourceType, ResourceID: resourceID, Response: encoded,
		CreatedAt: now, ExpiresAt: now.Add(idempotencyLifetime),
	}
	reserved, created, err := repositories.Idempotency().Reserve(ctx, record)
	if err != nil {
		return err
	}
	if !created {
		if reserved.RequestHash != requestHash {
			return storage.ErrIdempotencyMismatch
		}
		return storage.ErrConflict
	}
	metadata["authenticationType"] = request.authenticationType
	metadata["reason"] = request.reason
	metadata["idempotencyKeyHash"] = request.keyHash
	auditMetadata, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return repositories.Audit().Append(ctx, storage.AuditEvent{
		ID: service.newID(), PrincipalID: request.principalID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, Outcome: "success",
		RequestID: request.requestID, Metadata: auditMetadata, CreatedAt: now,
	})
}

func requestDigest(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func taskVersion(updatedAt time.Time) uint64 {
	if updatedAt.IsZero() || updatedAt.UnixNano() <= 0 {
		return 0
	}
	return uint64(updatedAt.UTC().UnixNano())
}

func validUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

func mapError(err error) error {
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrIdempotencyMismatch) {
		return errors.Join(ErrConflict, err)
	}
	return err
}
