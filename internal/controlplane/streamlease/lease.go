package streamlease

import (
	"context"
	"errors"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/periodic"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type Store interface {
	OAuthSessions() storage.OAuthSessionRepository
	Sessions() storage.SessionRepository
	Tasks() storage.TaskRepository
}

type Config struct {
	Now           func() time.Time
	CheckInterval time.Duration
	Runtime       RuntimeRegistry
	TaskID        string
	HeartbeatTask bool
	Authorizer    authorization.Authorizer
	Authorization authorization.Request
}

type RuntimeRegistry interface {
	AttachRuntime(
		context.Context,
		string,
		string,
	) (context.Context, func(), error)
}

func RuntimeFrom(value any) RuntimeRegistry {
	runtime, _ := value.(RuntimeRegistry)
	return runtime
}

func Start(
	parent context.Context,
	store Store,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	config Config,
) (context.Context, context.CancelFunc, error) {
	if parent == nil || store == nil {
		return nil, nil, errors.New(
			"authorization lease context and store are required",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.CheckInterval == 0 {
		config.CheckInterval = 500 * time.Millisecond
	}
	if config.CheckInterval < 10*time.Millisecond ||
		config.CheckInterval > 30*time.Second {
		return nil, nil, errors.New(
			"authorization lease check interval must be between 10ms and 30s",
		)
	}
	if config.HeartbeatTask && config.TaskID == "" {
		return nil, nil, errors.New(
			"task identity is required for owner heartbeat",
		)
	}
	now := config.Now().UTC()
	if !session.ExpiresAt.After(now) ||
		(!identity.AccessExpiresAt.IsZero() && !identity.AccessExpiresAt.After(now)) {
		return nil, nil, errors.New("authorization lease expired")
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if identity.AccessExpiresAt.IsZero() || identity.AuthorizationID != "" {
		// The OAuth grant is checked throughout the stream, so a refreshed
		// login must not leave the WebSocket bound to the opening access token's
		// immutable expiry. Credentials without a Family retain that deadline.
		ctx, cancel = context.WithCancel(parent)
	} else {
		ctx, cancel = context.WithDeadline(parent, identity.AccessExpiresAt.UTC())
	}
	go watch(ctx, cancel, store, identity, session.ID, config)
	if config.Runtime != nil {
		runtimeContext, release, err := config.Runtime.AttachRuntime(
			ctx,
			session.ID,
			config.TaskID,
		)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		return runtimeContext, func() {
			cancel()
			release()
		}, nil
	}
	return ctx, cancel, nil
}

func watch(
	ctx context.Context,
	cancel context.CancelFunc,
	store Store,
	identity controlplaneapi.Identity,
	sessionID string,
	config Config,
) {
	periodic.RunAfter(
		ctx,
		config.CheckInterval,
		func(ctx context.Context) {
			checkContext, checkCancel := context.WithTimeout(
				ctx,
				config.CheckInterval,
			)
			valid := true
			if config.Authorizer != nil {
				valid = config.Authorizer.Authorize(
					checkContext,
					authorization.Subject{
						ID:       identity.Subject,
						Provider: identity.Provider,
						Groups:   append([]string(nil), identity.Groups...),
					},
					config.Authorization,
				).Allowed
			}
			if identity.AuthorizationID != "" {
				identityID, deviceID, err := store.OAuthSessions().
					RequestOwner(checkContext, identity.AuthorizationID)
				active, activeErr := store.OAuthSessions().
					RequestActive(checkContext, identity.AuthorizationID, config.Now().UTC())
				now := config.Now().UTC()
				valid = err == nil && activeErr == nil && active &&
					identityID == identity.Subject &&
					deviceID == identity.DeviceID &&
					!now.IsZero()
			}
			if valid {
				storedSession, err := store.Sessions().
					GetByID(checkContext, sessionID)
				now := config.Now().UTC()
				valid = err == nil &&
					storedSession.IdentityID == identity.Subject &&
					storedSession.DeviceID == identity.DeviceID &&
					storedSession.State == statusActive &&
					storedSession.ExpiresAt.After(now)
			}
			if valid && config.HeartbeatTask {
				task, err := store.Tasks().GetByID(checkContext, config.TaskID)
				valid = err == nil && task.SessionID == sessionID &&
					task.IdentityID == identity.Subject &&
					task.State.Owned()
				if valid {
					err = store.Tasks().UpdateState(
						checkContext, task.ID, task.State, task.State, task.Result, config.Now().UTC(),
					)
					valid = err == nil || errors.Is(err, storage.ErrConflict)
				}
			}
			checkCancel()
			if !valid {
				cancel()
			}
		},
	)
}
