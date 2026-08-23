package routequery

import (
	"io"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func RequireEmptyBody(request *http.Request) *controlplaneapi.Error {
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "request body is invalid",
		}
	}
	if len(contents) != 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Message: "request body must be empty",
		}
	}
	return nil
}
