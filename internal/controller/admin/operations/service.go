// Package operations implements durable, audited Management Plane actions.
// Database state is committed before best-effort runtime convergence begins.
package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest = errors.New("management operation request is invalid")
	ErrConflict       = errors.New("management operation precondition failed")
)

const (
	idempotencyLifetime = 24 * time.Hour
	runtimeTimeout      = 5 * time.Second
)

type Store interface {
	storage.Repositories
	storage.TransactionManager
}

type SessionRuntime interface {
	Disconnect(context.Context, string) error
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

type RevokeDeviceSessionRequest struct {
	Request
	PrincipalID     string
	DeviceSessionID string
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

type RevocationResult struct {
	PrincipalID     string    `json:"principalId"`
	DeviceSessionID string    `json:"deviceSessionId,omitempty"`
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

type Service struct {
	store   Store
	runtime SessionRuntime
	now     func() time.Time
	newID   func() string
}

func New(store Store, runtime SessionRuntime) (*Service, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("management operation storage and Session runtime are required")
	}
	return &Service{store: store, runtime: runtime, now: time.Now, newID: uuid.NewString}, nil
}

func (service *Service) RevokeDeviceSession(ctx context.Context, request RevokeDeviceSessionRequest) (RevocationResult, error) {
	common, err := normalizeRequest(request.Request, "admin.device-session.revoke")
	if err != nil || !validUUID(request.PrincipalID) || !validUUID(request.DeviceSessionID) {
		return RevocationResult{}, ErrInvalidRequest
	}
	requestHash := requestDigest(struct {
		PrincipalID string `json:"principalId"`
		SessionID   string `json:"deviceSessionId"`
		Reason      string `json:"reason"`
	}{request.PrincipalID, request.DeviceSessionID, common.reason})
	result := RevocationResult{}
	err = service.store.WithinTransaction(ctx, func(repositories storage.Repositories) error {
		replayed, lookupErr := replay(repositories, ctx, common.scope, common.keyHash, requestHash, &result)
		if lookupErr != nil || replayed {
			if replayed {
				result.Replayed = true
			}
			return lookupErr
		}
		family, getErr := repositories.TokenFamilies().GetByID(ctx, request.DeviceSessionID)
		if getErr != nil {
			return getErr
		}
		if family.PrincipalID != request.PrincipalID {
			return storage.ErrNotFound
		}
		revokedAt := service.now().UTC()
		count := int64(1)
		if family.RevokedAt != nil {
			revokedAt, count = family.RevokedAt.UTC(), 0
		} else if revokeErr := repositories.TokenFamilies().Revoke(ctx, family.ID, revokedAt); revokeErr != nil {
			return revokeErr
		}
		result = RevocationResult{
			PrincipalID: family.PrincipalID, DeviceSessionID: family.ID,
			RevokedCount: count, RevokedAt: revokedAt,
		}
		return service.persistSuccess(ctx, repositories, common, requestHash, "device-session", family.ID,
			"admin.device-session.revoke", map[string]any{"targetPrincipalId": family.PrincipalID}, result)
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
		count, revokeErr := repositories.TokenFamilies().RevokeByPrincipal(ctx, request.PrincipalID, revokedAt)
		if revokeErr != nil {
			return revokeErr
		}
		result = RevocationResult{PrincipalID: request.PrincipalID, RevokedCount: count, RevokedAt: revokedAt}
		return service.persistSuccess(ctx, repositories, common, requestHash, "principal", request.PrincipalID,
			"admin.principal.revoke", map[string]any{"revokedDeviceSessionCount": count}, result)
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
