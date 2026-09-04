package kubeapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/transport/websocketio"
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
	connection, err := websocketio.Upgrade(writer, request)
	if err != nil {
		return nil
	}
	defer func() { _ = connection.Close() }()
	for {
		select {
		case <-watchContext.Done():
			_ = websocketio.Close(
				connection, websocket.CloseNormalClosure,
				"Inventory Watch closed",
			)
			return nil
		case snapshot := <-updates:
			encoded, encodeErr := json.Marshal(snapshot)
			if encodeErr != nil {
				_ = websocketio.Close(
					connection, websocket.CloseInternalServerErr,
					"Inventory Watch encoding failed",
				)
				return nil
			}
			if writeErr := websocketio.Write(
				watchContext,
				connection,
				websocket.TextMessage,
				encoded,
			); writeErr != nil {
				return nil
			}
		}
	}
}
