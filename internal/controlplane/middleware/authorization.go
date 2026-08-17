package middleware

import (
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
)

func authorizationRequestForHTTP(request *http.Request, apiPathPrefix string) authorization.Request {
	path := strings.TrimPrefix(request.URL.Path, apiPathPrefix)
	parts := strings.FieldsFunc(path, func(character rune) bool { return character == '/' })
	result := authorization.Request{Namespace: strings.TrimSpace(request.URL.Query().Get("namespace"))}
	if len(parts) == 0 {
		result.ResourceKind = "api"
	} else if parts[0] == "namespaces" {
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
	} else {
		result.ResourceKind = strings.ToLower(parts[0])
		if len(parts) >= 2 {
			result.ResourceName = parts[1]
		}
	}
	switch request.Method {
	case http.MethodGet:
		if request.URL.Query().Get("watch") == "true" {
			result.Operation = "watch"
		} else if result.ResourceName != "" {
			result.Operation = "get"
		} else {
			result.Operation = "list"
		}
	case http.MethodPost:
		result.Operation = "create"
	case http.MethodPut:
		result.Operation = "update"
	case http.MethodPatch:
		result.Operation = "patch"
	case http.MethodDelete:
		result.Operation = "delete"
	default:
		result.Operation = strings.ToLower(request.Method)
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "heartbeat" {
		result.Operation = "heartbeat"
	}
	if len(parts) == 3 && parts[0] == "sessions" && parts[2] == "tickets" {
		result.ResourceKind = "relay-tickets"
		result.ResourceName = parts[1]
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "port-forwards" {
		result.ResourceKind = "port-forwards"
		result.ResourceName = parts[1]
		if len(parts) == 3 && request.Method == http.MethodGet {
			result.Operation = "list"
		}
	}
	if len(parts) >= 3 && parts[0] == "sessions" && parts[2] == "exec" {
		result.ResourceKind = "pod-exec"
		result.ResourceName = parts[1]
		if len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
			result.ResourceName = parts[3]
		}
	}
	for _, resourceKind := range []string{"file-transfers", "exchanges", "mirrors", "previews"} {
		if len(parts) < 3 || parts[0] != "sessions" || parts[2] != resourceKind {
			continue
		}
		result.ResourceKind = resourceKind
		result.ResourceName = parts[1]
		if len(parts) >= 4 {
			result.ResourceName = parts[3]
		}
		if resourceKind == "file-transfers" && len(parts) == 5 && parts[4] == "stream" && request.Method == http.MethodGet {
			result.Operation = "stream"
		}
	}
	if len(parts) >= 4 && parts[0] == "sessions" && parts[2] == "pod-files" {
		result.ResourceKind = "pod-files"
		result.ResourceName = parts[1]
		switch parts[3] {
		case "list":
			result.Operation = "list"
		case "create":
			result.Operation = "create"
		case "rename":
			result.Operation = "update"
		case "delete":
			result.Operation = "delete"
		case "operations":
			result.Operation = "get"
			if len(parts) >= 5 {
				result.ResourceName = parts[4]
			}
		}
	}
	return result
}
