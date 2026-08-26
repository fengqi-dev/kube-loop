package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func authorizationRequestForHTTP(
	request *http.Request,
	apiPathPrefix string,
) authorization.Request {
	path := strings.TrimPrefix(request.URL.Path, apiPathPrefix)
	parts := strings.FieldsFunc(
		path,
		func(character rune) bool { return character == '/' },
	)
	result := authorizationResource(
		parts,
		strings.TrimSpace(request.URL.Query().Get("namespace")),
	)
	result.Operation = authorizationOperation(
		request,
		result.ResourceName != "",
	)
	applySessionAuthorization(&result, request, parts)
	return result
}

func authorizationResource(
	parts []string,
	namespace string,
) authorization.Request {
	result := authorization.Request{Namespace: namespace}
	switch {
	case len(parts) == 0:
		result.ResourceKind = "api"
	case parts[0] == "namespaces":
		result.ResourceKind = "namespaces"
		if len(parts) == 2 {
			result.ResourceName = parts[1]
		} else if len(parts) >= 3 {
			result.Namespace = parts[1]
			result.ResourceKind = strings.ToLower(parts[2])
			if len(parts) >= 4 {
				result.ResourceName = parts[3]
			}
		}
	default:
		result.ResourceKind = strings.ToLower(parts[0])
		if len(parts) >= 2 {
			result.ResourceName = parts[1]
		}
	}
	return result
}

func authorizationOperation(request *http.Request, namedResource bool) string {
	switch request.Method {
	case http.MethodGet:
		if request.URL.Query().Get("watch") == "true" {
			return "watch"
		}
		if namedResource {
			return "get"
		}
		return operationList
	case http.MethodPost:
		return operationCreate
	case http.MethodPut:
		return "update"
	case http.MethodPatch:
		return "patch"
	case http.MethodDelete:
		return operationDelete
	default:
		return strings.ToLower(request.Method)
	}
}

func applySessionAuthorization(
	result *authorization.Request,
	request *http.Request,
	parts []string,
) {
	applySessionCoreAuthorization(result, request, parts)
	applySessionTaskAuthorization(result, request, parts)
	applyPodFilesAuthorization(result, parts)
}

func applySessionCoreAuthorization(
	result *authorization.Request,
	request *http.Request,
	parts []string,
) {
	if len(parts) >= 3 && parts[0] == resourceSessions &&
		parts[2] == "heartbeat" {
		result.Operation = "heartbeat"
	}
	if len(parts) == 3 && parts[0] == resourceSessions &&
		parts[2] == "tickets" {
		result.ResourceKind = "relay-tickets"
		result.ResourceName = parts[1]
	}
	if len(parts) >= 3 && parts[0] == resourceSessions &&
		parts[2] == "port-forwards" {
		result.ResourceKind = "port-forwards"
		result.ResourceName = parts[1]
		if len(parts) == 3 && request.Method == http.MethodGet {
			result.Operation = operationList
		}
	}
	if len(parts) >= 3 && parts[0] == resourceSessions && parts[2] == "exec" {
		result.ResourceKind = "pod-exec"
		result.ResourceName = parts[1]
		if len(parts) == 5 && parts[4] == operationStream &&
			request.Method == http.MethodGet {
			result.Operation = operationStream
			result.ResourceName = parts[3]
		}
	}
}

func applySessionTaskAuthorization(
	result *authorization.Request,
	request *http.Request,
	parts []string,
) {
	for _, resourceKind := range []string{"file-transfers", "exchanges", "mirrors", "previews"} {
		if len(parts) < 3 || parts[0] != resourceSessions ||
			parts[2] != resourceKind {
			continue
		}
		result.ResourceKind = resourceKind
		result.ResourceName = parts[1]
		if len(parts) >= 4 {
			result.ResourceName = parts[3]
		}
		if resourceKind == "file-transfers" && len(parts) == 5 &&
			parts[4] == operationStream &&
			request.Method == http.MethodGet {
			result.Operation = operationStream
		}
	}
}

func applyPodFilesAuthorization(result *authorization.Request, parts []string) {
	if len(parts) >= 4 && parts[0] == resourceSessions &&
		parts[2] == "pod-files" {
		result.ResourceKind = "pod-files"
		result.ResourceName = parts[1]
		switch parts[3] {
		case operationList:
			result.Operation = operationList
		case operationCreate:
			result.Operation = operationCreate
		case "rename":
			result.Operation = "update"
		case operationDelete:
			result.Operation = operationDelete
		case "operations":
			result.Operation = "get"
			if len(parts) >= 5 {
				result.ResourceName = parts[4]
			}
		}
	}
}

func authorizeIdentity(
	ctx context.Context,
	authorizer authorization.Authorizer,
	identity controlplaneapi.Identity,
	request authorization.Request,
) (authorization.Decision, *controlplaneapi.Error) {
	if strings.TrimSpace(identity.Subject) == "" {
		return authorization.Decision{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnauthenticated,
			Message: authenticationRequiredMessage,
		}
	}
	decision := authorizer.Authorize(
		ctx,
		authorization.Subject{
			ID:       identity.Subject,
			Provider: identity.Provider,
			Groups:   append([]string(nil), identity.Groups...),
		},
		request,
	)
	if !decision.Allowed {
		return authorization.Decision{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "operation is not permitted",
		}
	}
	return decision, nil
}
