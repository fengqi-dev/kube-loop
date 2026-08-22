package sessionapi

import (
	"context"
	"errors"
	"uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func (handler *Service) RequireActive(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) (ActiveSession, *controlplaneapi.Error) {
	session, apiError := handler.loadOwned(ctx, identity, namespace, id)
	if apiError != nil {
		return ActiveSession{}, apiError
	}
	if session.State != sessionStateActive || !session.ExpiresAt.After(handler.now().UTC()) {
		return ActiveSession{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session is not active",
		}
	}
	if session.NetworkSpecHash == "" {
		return ActiveSession{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session has no NetworkSpec",
		}
	}
	if err := handler.registry.Ensure(session.ID); err != nil {
		return ActiveSession{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Session runtime is unavailable",
			Cause:   err,
		}
	}
	return ActiveSession{
		ID: session.ID, Namespace: session.Namespace, Generation: session.Generation, ExpiresAt: session.ExpiresAt,
		NetworkSpecHash: session.NetworkSpecHash,
	}, nil
}
func (handler *Service) loadOwned(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace, id string,
) (storage.Session, *controlplaneapi.Error) {
	if _, err := uuid.Parse(id); err != nil {
		return storage.Session{}, notFound()
	}
	session, err := handler.storage.Sessions().GetByID(ctx, id)
	if err != nil || !ownedBy(session, identity, namespace) {
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return storage.Session{}, mapStorageError(err)
		}
		return storage.Session{}, notFound()
	}
	now := handler.now().UTC()
	if session.State == sessionStateActive && !session.ExpiresAt.After(now) {
		updateErr := handler.storage.Sessions().UpdateState(
			ctx,
			session.ID,
			session.Generation,
			"expired",
			now,
		)
		if updateErr != nil && !errors.Is(updateErr, storage.ErrConflict) {
			return storage.Session{}, mapStorageError(updateErr)
		}
		session, err = handler.storage.Sessions().GetByID(ctx, session.ID)
		if err != nil {
			return storage.Session{}, mapStorageError(err)
		}
	}
	if session.State != sessionStateActive {
		if apiError := handler.disconnectRuntime(ctx, session.ID); apiError != nil {
			return storage.Session{}, apiError
		}
	}
	return session, nil
}

func ownedBy(session storage.Session, identity controlplaneapi.Identity, namespace string) bool {
	return session.IdentityID == identity.Subject && session.DeviceID == identity.DeviceID &&
		session.Namespace == namespace
}
