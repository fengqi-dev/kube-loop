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

type checker struct {
	store     Store
	identity  controlplaneapi.Identity
	sessionID string
	config    Config
}

const validationErrorsBeforeCancellation = 2

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
	leaseContext := ctx
	var releaseRuntime func()
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
		leaseContext = runtimeContext
		releaseRuntime = release
	}
	go watch(ctx, cancel, store, identity, session.ID, config)
	if releaseRuntime != nil {
		return leaseContext, func() {
			cancel()
			releaseRuntime()
		}, nil
	}
	return leaseContext, cancel, nil
}

func watch(
	ctx context.Context,
	cancel context.CancelFunc,
	store Store,
	identity controlplaneapi.Identity,
	sessionID string,
	config Config,
) {
	lease := checker{
		store: store, identity: identity, sessionID: sessionID, config: config,
	}
	watchValidation(ctx, cancel, config.CheckInterval, lease.valid)
}

func watchValidation(
	ctx context.Context,
	cancel context.CancelFunc,
	checkInterval time.Duration,
	validate func(context.Context) (bool, error),
) {
	consecutiveErrors := 0
	periodic.RunAfter(
		ctx,
		checkInterval,
		func(ctx context.Context) {
			checkContext, checkCancel := context.WithTimeout(
				ctx,
				checkInterval,
			)
			valid, err := validate(checkContext)
			checkCancel()
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= validationErrorsBeforeCancellation {
					cancel()
				}
				return
			}
			consecutiveErrors = 0
			if !valid {
				cancel()
			}
		},
	)
}

func (lease checker) valid(ctx context.Context) (bool, error) {
	if !lease.authorized(ctx) {
		return false, nil
	}
	checks := []func(context.Context) (bool, error){
		lease.grantActive,
		lease.sessionActive,
		lease.taskOwned,
	}
	for _, check := range checks {
		valid, err := check(ctx)
		if err != nil || !valid {
			return valid, err
		}
	}
	return true, nil
}

func (lease checker) authorized(ctx context.Context) bool {
	if lease.config.Authorizer == nil {
		return true
	}
	return lease.config.Authorizer.Authorize(
		ctx,
		authorization.Subject{
			ID:       lease.identity.Subject,
			Provider: lease.identity.Provider,
			Groups:   append([]string(nil), lease.identity.Groups...),
		},
		lease.config.Authorization,
	).Allowed
}

func (lease checker) grantActive(ctx context.Context) (bool, error) {
	if lease.identity.AuthorizationID == "" {
		return true, nil
	}
	identityID, deviceID, err := lease.store.OAuthSessions().
		RequestOwner(ctx, lease.identity.AuthorizationID)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	active, activeErr := lease.store.OAuthSessions().RequestActive(
		ctx,
		lease.identity.AuthorizationID,
		lease.config.Now().UTC(),
	)
	if activeErr != nil {
		return false, activeErr
	}
	now := lease.config.Now().UTC()
	return active &&
		identityID == lease.identity.Subject &&
		deviceID == lease.identity.DeviceID &&
		!now.IsZero(), nil
}

func (lease checker) sessionActive(ctx context.Context) (bool, error) {
	stored, err := lease.store.Sessions().GetByID(ctx, lease.sessionID)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return stored.IdentityID == lease.identity.Subject &&
		stored.DeviceID == lease.identity.DeviceID &&
		stored.State == statusActive &&
		stored.ExpiresAt.After(lease.config.Now().UTC()), nil
}

func (lease checker) taskOwned(ctx context.Context) (bool, error) {
	if !lease.config.HeartbeatTask {
		return true, nil
	}
	task, err := lease.store.Tasks().GetByID(ctx, lease.config.TaskID)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if task.SessionID != lease.sessionID ||
		task.IdentityID != lease.identity.Subject ||
		!task.State.Owned() {
		return false, nil
	}
	err = lease.store.Tasks().UpdateState(
		ctx,
		task.ID,
		task.State,
		task.State,
		task.Result,
		lease.config.Now().UTC(),
	)
	if err == nil || errors.Is(err, storage.ErrConflict) {
		return true, nil
	}
	return false, err
}
