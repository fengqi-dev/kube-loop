package mirrorapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

const mirrorSnapshotKind = "service-intercept"

var errTaskStopRequested = errors.New("Mirror Task stop requested")

type ownerResult struct {
	OwnerID   string `json:"ownerId"`
	GatewayIP string `json:"gatewayIp"`
	Restored  bool   `json:"restored,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (handler *Handler) stream(
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
	session sessionapi.ActiveSession,
	taskID string,
) *controller.APIError {
	task, apiError := handler.ownedTask(request.Context(), principal, session, taskID)
	if apiError != nil {
		return apiError
	}
	if task.State != remotetask.Pending {
		return &controller.APIError{Code: controller.CodeConflict, Message: "Mirror Task was already claimed"}
	}
	var spec storedSpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil || spec.Service == "" || len(spec.Ports) == 0 {
		return internalError(errors.New("stored Mirror Task is invalid"))
	}
	claim := ownerResult{OwnerID: handler.config.OwnerID, GatewayIP: handler.config.GatewayIP}
	claimJSON, _ := json.Marshal(claim)
	if err := handler.storage.Tasks().UpdateState(
		request.Context(), task.ID, remotetask.Pending, remotetask.Starting, claimJSON, handler.now().UTC(),
	); err != nil {
		return storageError(err)
	}

	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		handler.finishMirror(task.ID, false, false, errors.New("WebSocket upgrade failed"))
		return nil
	}
	defer connection.CloseNow()
	connection.SetReadLimit(mirrorstream.MaximumData + mirrorstream.HeaderSize)

	leaseContext, leaseCancel, err := streamlease.Start(
		request.Context(), handler.storage, principal, session,
		streamlease.Config{
			Now: handler.now, CheckInterval: handler.config.CredentialCheckInterval,
			Runtime: streamlease.RuntimeFrom(handler.sessions), TaskID: task.ID,
		},
	)
	if err != nil {
		handler.finishMirror(task.ID, false, false, err)
		_ = connection.Close(websocket.StatusPolicyViolation, "authorization lease expired")
		return nil
	}
	defer leaseCancel()
	runContext, cancel := context.WithCancelCause(leaseContext)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		handler.watchMirrorTask(runContext, cancel, task.ID, claimJSON)
	}()

	listeners, err := bindMirrorListeners(handler.config.GatewayIP, spec.Ports)
	if err != nil {
		cancel(err)
		<-watchDone
		handler.finishMirror(task.ID, false, false, err)
		_ = connection.Close(websocket.StatusInternalError, "Mirror listener allocation failed")
		return nil
	}
	defer listeners.Close()
	snapshot := servicebinding.ServiceInterceptSnapshot{
		Namespace: session.Namespace, Service: spec.Service, GatewayIP: handler.config.GatewayIP,
		Ports: append([]servicebinding.InterceptPort(nil), listeners.mappings...),
	}
	snapshotPersisted := false
	mutationAttempted := false
	var primaries *primaryPool
	preparationErr := handler.resources.Capture(runContext, principal, &snapshot)
	if preparationErr == nil {
		var backendSets []servicebinding.BackendSet
		backendSets, preparationErr = servicebinding.ResolveSnapshotBackends(snapshot)
		if preparationErr == nil {
			primaries, preparationErr = newPrimaryPool(backendSets, handler.config.PrimaryDialContext)
		}
	}
	if preparationErr == nil {
		var encoded []byte
		encoded, preparationErr = json.Marshal(snapshot)
		if preparationErr == nil {
			now := handler.now().UTC()
			preparationErr = handler.storage.WithinTransaction(runContext, func(repositories storage.Repositories) error {
				if err := repositories.Tasks().UpdateState(runContext, task.ID, remotetask.Starting, remotetask.Starting, claimJSON, now); err != nil {
					return err
				}
				return repositories.ResourceSnapshots().Put(runContext, storage.ResourceSnapshot{
					ID: uuid.NewString(), TaskID: task.ID, Kind: mirrorSnapshotKind,
					Namespace: session.Namespace, Name: spec.Service, Data: encoded, CreatedAt: now,
				})
			})
			if preparationErr == nil {
				snapshotPersisted = true
			}
		}
	}
	if preparationErr == nil && runContext.Err() == nil {
		mutationAttempted = true
		preparationErr = handler.resources.Apply(runContext, principal, snapshot, task.ID)
	}
	if preparationErr == nil && runContext.Err() == nil {
		preparationErr = handler.storage.Tasks().UpdateState(
			runContext, task.ID, remotetask.Starting, remotetask.Running, claimJSON, handler.now().UTC(),
		)
	}
	if preparationErr == nil && runContext.Err() == nil {
		relay := newMirrorRelay(connection, listeners, primaries, handler.config)
		readyContext, readyCancel := context.WithTimeout(runContext, handler.config.ShadowWriteTimeout)
		preparationErr = relay.writeFrame(readyContext, mirrorstream.Frame{Type: mirrorstream.Ready})
		readyCancel()
		if preparationErr == nil {
			preparationErr = relay.run(runContext)
		}
	}

	failure := mirrorFailure(preparationErr, runContext)
	cancel(preparationErr)
	_ = listeners.Close()
	<-watchDone
	restoreErr := error(nil)
	if mutationAttempted {
		restoreContext, restoreCancel := context.WithTimeout(context.Background(), handler.config.RestoreTimeout)
		restoreErr = handler.resources.Restore(restoreContext, snapshot, task.ID)
		restoreCancel()
	}
	failure = failure || restoreErr != nil
	finished := handler.finishMirror(task.ID, snapshotPersisted, restoreErr == nil, errors.Join(preparationErr, restoreErr))
	if finished && snapshotPersisted && restoreErr == nil {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = handler.storage.ResourceSnapshots().DeleteByTask(cleanupContext, task.ID)
		cleanupCancel()
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	stopFrame, _ := mirrorstream.Encode(mirrorstream.Frame{Type: mirrorstream.Stop})
	_ = connection.Write(closeContext, websocket.MessageBinary, stopFrame)
	closeCancel()
	if failure {
		_ = connection.Close(websocket.StatusInternalError, "Mirror stream failed")
	} else {
		_ = connection.Close(websocket.StatusNormalClosure, "Mirror stopped")
	}
	return nil
}

func (handler *Handler) watchMirrorTask(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	taskID string,
	owner json.RawMessage,
) {
	ticker := time.NewTicker(handler.config.TaskCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkContext, checkCancel := context.WithTimeout(ctx, handler.config.TaskCheckInterval)
			task, err := handler.storage.Tasks().GetByID(checkContext, taskID)
			if err == nil {
				switch task.State {
				case remotetask.Starting, remotetask.Running:
					err = handler.storage.Tasks().UpdateState(
						checkContext, taskID, task.State, task.State, owner, handler.now().UTC(),
					)
					if errors.Is(err, storage.ErrConflict) {
						err = nil
					}
				case remotetask.Stopping, remotetask.Stopped, remotetask.Failed:
					err = errTaskStopRequested
				default:
					err = errors.New("Mirror Task entered an invalid state")
				}
			}
			checkCancel()
			if err != nil {
				cancel(err)
				return
			}
		}
	}
}

func (handler *Handler) finishMirror(taskID string, snapshotPersisted, restored bool, cause error) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := handler.storage.Tasks().GetByID(ctx, taskID)
	if err != nil {
		return false
	}
	next := remotetask.Stopped
	if task.State != remotetask.Stopping && cause != nil && !errors.Is(cause, context.Canceled) &&
		!errors.Is(cause, context.DeadlineExceeded) && !errors.Is(cause, errTaskStopRequested) &&
		!errors.Is(cause, errClientStopped) && websocket.CloseStatus(cause) == -1 {
		next = remotetask.Failed
	}
	if task.State == remotetask.Stopped || task.State == remotetask.Failed {
		return true
	}
	if task.State == remotetask.Recovering {
		return false
	}
	result := ownerResult{OwnerID: handler.config.OwnerID, GatewayIP: handler.config.GatewayIP, Restored: restored}
	if next == remotetask.Failed {
		result.Error = "Mirror stream failed"
	}
	if snapshotPersisted && !restored {
		result.Error = "Mirror resource restoration is pending"
		// Keep the Task in the recovery work queue. A terminal failed state
		// would retain the durable snapshot but make it invisible to the stale
		// owner reconciler, leaving the Service intercepted indefinitely.
		next = remotetask.Recovering
	}
	encoded, _ := json.Marshal(result)
	return handler.storage.Tasks().UpdateState(ctx, taskID, task.State, next, encoded, handler.now().UTC()) == nil
}

func mirrorFailure(err error, runContext context.Context) bool {
	if err == nil || runContext.Err() != nil || errors.Is(err, errClientStopped) || websocket.CloseStatus(err) != -1 {
		return false
	}
	return true
}
