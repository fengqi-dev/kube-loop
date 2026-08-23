package kubeapi

import (
	"context"
	"encoding/json"
	"net/http"

	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

func (handler *Service) watchInventory(
	writer http.ResponseWriter,
	request *http.Request,
	client kubernetes.Interface,
	identity controlplaneapi.Identity,
	namespace string,
	resource inventoryResource,
) *controlplaneapi.Error {
	if len(request.URL.Query()) != 1 ||
		len(request.URL.Query()[operationWatch]) != 1 ||
		request.URL.Query().Get(operationWatch) != "true" {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   operationWatch,
			Message: "watch=true is required",
		}
	}
	watchContext := request.Context()
	cancel := func() {}
	if !identity.AccessExpiresAt.IsZero() {
		watchContext, cancel = context.WithDeadline(
			watchContext,
			identity.AccessExpiresAt,
		)
	}
	defer cancel()
	updates, unsubscribe, err := handler.inventory.subscribe(
		watchContext,
		authorization.Subject{
			ID: identity.Subject, Groups: append([]string(nil), identity.Groups...),
		},
		client,
		namespace,
		resource,
	)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Inventory Watch is unavailable",
			Cause:   err,
		}
	}
	defer unsubscribe()
	connection, err := websocket.Accept(
		writer,
		request,
		&websocket.AcceptOptions{
			CompressionMode: websocket.CompressionDisabled,
		},
	)
	if err != nil {
		return nil
	}
	defer func() { _ = connection.CloseNow() }()
	for {
		select {
		case <-watchContext.Done():
			_ = connection.Close(
				websocket.StatusNormalClosure,
				"Inventory Watch closed",
			)
			return nil
		case snapshot := <-updates:
			encoded, encodeErr := json.Marshal(snapshot)
			if encodeErr != nil {
				_ = connection.Close(
					websocket.StatusInternalError,
					"Inventory Watch encoding failed",
				)
				return nil
			}
			if writeErr := connection.Write(watchContext, websocket.MessageText, encoded); writeErr != nil {
				return nil
			}
		}
	}
}
