package previewapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/exchangeapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/controller/streamlease"
	"github.com/fengqi-dev/kube-loop/internal/protocol/exchangestream"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	"github.com/google/uuid"
)

const previewSnapshotKind = "preview-service"

var errTaskStopRequested = errors.New("Preview Task stop requested")

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
		return &controller.APIError{Code: controller.CodeConflict, Message: "Preview Task was already claimed"}
	}
	var spec storedSpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil || spec.Name == "" || len(spec.Ports) == 0 {
		return internalError(errors.New("stored Preview Task is invalid"))
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
		handler.finishPreview(task.ID, false, false, false, errors.New("WebSocket upgrade failed"))
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
		handler.finishPreview(task.ID, false, false, false, err)
		_ = connection.Close(websocket.StatusPolicyViolation, "authorization lease expired")
		return nil
	}
	defer leaseCancel()
	runContext, cancel := context.WithCancelCause(leaseContext)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		handler.watchPreviewTask(runContext, cancel, task.ID, claimJSON)
	}()

	listeners, err := exchangeapi.BindReverseListeners(handler.config.GatewayIP, spec.Ports)
	if err != nil {
		cancel(err)
		<-watchDone
		handler.finishPreview(task.ID, false, false, false, err)
		_ = connection.Close(websocket.StatusInternalError, "Preview listener allocation failed")
		return nil
	}
	defer listeners.Close()
	snapshot := servicebinding.PreviewServiceSnapshot{
		Namespace: session.Namespace, Service: spec.Name, GatewayIP: handler.config.GatewayIP,
		Ports: listeners.Mappings(),
	}
	snapshotPersisted := false
	cleanupRequired := false
	preparationErr := error(nil)
	encoded, preparationErr := json.Marshal(snapshot)
	if preparationErr == nil {
		now := handler.now().UTC()
		preparationErr = handler.storage.WithinTransaction(runContext, func(repositories storage.Repositories) error {
			if err := repositories.Tasks().UpdateState(runContext, task.ID, remotetask.Starting, remotetask.Starting, claimJSON, now); err != nil {
				return err
			}
			return repositories.ResourceSnapshots().Put(runContext, storage.ResourceSnapshot{
				ID: uuid.NewString(), TaskID: task.ID, Kind: previewSnapshotKind,
				Namespace: session.Namespace, Name: spec.Name, Data: encoded, CreatedAt: now,
			})
		})
		if preparationErr == nil {
			snapshotPersisted = true
		}
	}
	if preparationErr == nil && runContext.Err() == nil {
		var serviceClusterIP string
		service, createErr := handler.resources.Create(runContext, principal, snapshot, task.ID)
		preparationErr = createErr
		cleanupRequired = createErr == nil || errors.Is(createErr, servicebinding.ErrPreviewCleanupPending)
		if createErr == nil {
			if service == nil || service.Spec.ClusterIP == "" {
				preparationErr = errors.New("created Preview Service has no ClusterIP")
			} else {
				serviceClusterIP = service.Spec.ClusterIP
				claim.ClusterIP = serviceClusterIP
			}
		}
	}
	if preparationErr == nil && runContext.Err() == nil {
		runningResult, _ := json.Marshal(claim)
		preparationErr = handler.storage.Tasks().UpdateState(
			runContext, task.ID, remotetask.Starting, remotetask.Running, runningResult, handler.now().UTC(),
		)
	}
	if preparationErr == nil && runContext.Err() == nil {
		preparationErr = exchangeapi.WriteReverseFrame(runContext, connection, exchangestream.Frame{Type: exchangestream.Ready})
		if preparationErr == nil {
			preparationErr = exchangeapi.RunReverseRelay(
				runContext, connection, listeners, handler.config.UDPIdleTimeout, handler.now,
			)
		}
	}

	failure := previewFailure(preparationErr, runContext)
	cancel(preparationErr)
	_ = listeners.Close()
	<-watchDone
	deleteErr := error(nil)
	deleted := !cleanupRequired
	if cleanupRequired {
		deleteContext, deleteCancel := context.WithTimeout(context.Background(), handler.config.DeleteTimeout)
		deleteErr = handler.resources.Delete(deleteContext, snapshot, task.ID)
		deleteCancel()
		deleted = deleteErr == nil
	}
	failure = failure || deleteErr != nil
	finished := handler.finishPreview(
		task.ID, snapshotPersisted, cleanupRequired, deleted, errors.Join(preparationErr, deleteErr),
	)
	if finished && snapshotPersisted && deleted {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = handler.storage.ResourceSnapshots().DeleteByTask(cleanupContext, task.ID)
		cleanupCancel()
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	stopFrame, _ := exchangestream.Encode(exchangestream.Frame{Type: exchangestream.Stop})
	_ = connection.Write(closeContext, websocket.MessageBinary, stopFrame)
	closeCancel()
	if failure {
		_ = connection.Close(websocket.StatusInternalError, "Preview stream failed")
	} else {
		_ = connection.Close(websocket.StatusNormalClosure, "Preview stopped")
	}
	return nil
}

func (handler *Handler) watchPreviewTask(
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
					heartbeat := owner
					var current ownerResult
					if json.Unmarshal(task.Result, &current) == nil && current.ClusterIP != "" {
						heartbeat = task.Result
					}
					err = handler.storage.Tasks().UpdateState(
						checkContext, taskID, task.State, task.State, heartbeat, handler.now().UTC(),
					)
					if errors.Is(err, storage.ErrConflict) {
						err = nil
					}
				case remotetask.Stopping, remotetask.Stopped, remotetask.Failed:
					err = errTaskStopRequested
				default:
					err = errors.New("Preview Task entered an invalid state")
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

func (handler *Handler) finishPreview(
	taskID string,
	snapshotPersisted, cleanupRequired, deleted bool,
	cause error,
) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := handler.storage.Tasks().GetByID(ctx, taskID)
	if err != nil {
		return false
	}
	next := remotetask.Stopped
	if task.State != remotetask.Stopping && cause != nil && !errors.Is(cause, context.Canceled) &&
		!errors.Is(cause, context.DeadlineExceeded) && !errors.Is(cause, errTaskStopRequested) &&
		!errors.Is(cause, exchangeapi.ErrReverseClientStopped) && websocket.CloseStatus(cause) == -1 {
		next = remotetask.Failed
	}
	if task.State == remotetask.Stopped || task.State == remotetask.Failed {
		return true
	}
	if task.State == remotetask.Recovering {
		return false
	}
	result := ownerResult{OwnerID: handler.config.OwnerID, GatewayIP: handler.config.GatewayIP, Deleted: deleted}
	var previous ownerResult
	if json.Unmarshal(task.Result, &previous) == nil {
		result.ClusterIP = previous.ClusterIP
	}
	if next == remotetask.Failed {
		result.Error = "Preview stream failed"
	}
	if snapshotPersisted && cleanupRequired && !deleted {
		result.Error = "Preview resource deletion is pending"
		next = remotetask.Recovering
	}
	encoded, _ := json.Marshal(result)
	return handler.storage.Tasks().UpdateState(ctx, taskID, task.State, next, encoded, handler.now().UTC()) == nil
}

func previewFailure(err error, runContext context.Context) bool {
	if err == nil || runContext.Err() != nil || errors.Is(err, exchangeapi.ErrReverseClientStopped) || websocket.CloseStatus(err) != -1 {
		return false
	}
	return true
}
