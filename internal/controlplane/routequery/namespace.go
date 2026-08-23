package routequery

import (
	"net/http"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

const namespaceParameter = "namespace"

// Namespace accepts exactly one namespace query parameter and preserves the
// field-specific errors used by session and RelayTicket APIs.
func Namespace(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != namespaceParameter {
			return "", &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   key,
				Message: "query parameter is not supported",
			}
		}
		if len(values) != 1 {
			return "", &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   key,
				Message: "query parameter must be provided once",
			}
		}
	}
	namespace := query.Get(namespaceParameter)
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   namespaceParameter,
			Message: "namespace is invalid",
		}
	}
	return namespace, nil
}
