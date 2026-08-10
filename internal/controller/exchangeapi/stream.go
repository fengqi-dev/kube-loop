package exchangeapi

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
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

const exchangeSnapshotKind = "service-intercept"

var errTaskStopRequested = errors.New("Exchange Task stop requested")

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
		return &controller.APIError{Code: controller.CodeConflict, Message: "Exchange Task was already claimed"}
	}
	var spec storedSpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil || spec.Service == "" || len(spec.Ports) == 0 {
		return internalError(errors.New("stored Exchange Task is invalid"))
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
		handler.finishExchange(task.ID, false, false, errors.New("WebSocket upgrade failed"))
		return nil
	}
	defer connection.CloseNow()
	connection.SetReadLimit(exchangestream.MaximumData + 14)

	leaseContext, leaseCancel, err := streamlease.Start(
		request.Context(), handler.storage, principal, session,
		streamlease.Config{
			Now: handler.now, CheckInterval: handler.config.CredentialCheckInterval,
			Runtime: streamlease.RuntimeFrom(handler.sessions), TaskID: task.ID,
		},
	)
	if err != nil {
		handler.finishExchange(task.ID, false, false, err)
		_ = connection.Close(websocket.StatusPolicyViolation, "authorization lease expired")
		return nil
	}
	defer leaseCancel()
	runContext, cancel := context.WithCancelCause(leaseContext)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		handler.watchExchangeTask(runContext, cancel, task.ID, claimJSON)
	}()

	listeners, err := bindExchangeListeners(handler.config.GatewayIP, spec.Ports)
	if err != nil {
		cancel(err)
		<-watchDone
		handler.finishExchange(task.ID, false, false, err)
		_ = connection.Close(websocket.StatusInternalError, "Exchange listener allocation failed")
		return nil
	}
	defer listeners.Close()
	snapshot := servicebinding.ServiceInterceptSnapshot{
		Namespace: session.Namespace, Service: spec.Service, GatewayIP: handler.config.GatewayIP,
		Ports: append([]servicebinding.InterceptPort(nil), listeners.mappings...),
	}
	snapshotPersisted := false
	mutationAttempted := false
	preparationErr := handler.resources.Capture(runContext, principal, &snapshot)
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
					ID: uuid.NewString(), TaskID: task.ID, Kind: exchangeSnapshotKind,
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
		relay := newRelaySession(connection, listeners, handler.config.UDPIdleTimeout, handler.now)
		preparationErr = relay.write(runContext, exchangestream.Frame{Type: exchangestream.Ready})
		if preparationErr == nil {
			preparationErr = relay.run(runContext)
		}
	}

	failure := exchangeFailure(preparationErr, runContext)
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
	finished := handler.finishExchange(task.ID, snapshotPersisted, restoreErr == nil, errors.Join(preparationErr, restoreErr))
	if finished && snapshotPersisted && restoreErr == nil {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = handler.storage.ResourceSnapshots().DeleteByTask(cleanupContext, task.ID)
		cleanupCancel()
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	stopFrame, _ := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	_ = connection.Write(closeContext, websocket.MessageBinary, stopFrame)
	closeCancel()
	if failure {
		_ = connection.Close(websocket.StatusInternalError, "Exchange stream failed")
	} else {
		_ = connection.Close(websocket.StatusNormalClosure, "Exchange stopped")
	}
	return nil
}

func (handler *Handler) watchExchangeTask(
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
					err = errors.New("Exchange Task entered an invalid state")
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

func (handler *Handler) finishExchange(taskID string, snapshotPersisted, restored bool, cause error) bool {
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
		result.Error = "Exchange stream failed"
	}
	if snapshotPersisted && !restored {
		result.Error = "Exchange resource restoration is pending"
		// Keep the Task in the recovery work queue. A terminal failed state
		// would retain the durable snapshot but make it invisible to the stale
		// owner reconciler, leaving the Service intercepted indefinitely.
		next = remotetask.Recovering
	}
	encoded, _ := json.Marshal(result)
	return handler.storage.Tasks().UpdateState(ctx, taskID, task.State, next, encoded, handler.now().UTC()) == nil
}

func exchangeFailure(err error, runContext context.Context) bool {
	if err == nil || runContext.Err() != nil || errors.Is(err, errClientStopped) || websocket.CloseStatus(err) != -1 {
		return false
	}
	return true
}
