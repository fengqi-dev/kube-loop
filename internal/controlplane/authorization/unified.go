package authorization

import (
	"context"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
)

// UnifiedAuthorizer adapts Gateway request metadata to the same immutable
// capability policy used by the Management API. It contains no policy state.
type NamespaceLabelResolver func(context.Context, string) (map[string]string, error)

type UnifiedAuthorizer struct {
	engine        *adminauthorization.Engine
	resolveLabels NamespaceLabelResolver
}

func NewUnified(engine *adminauthorization.Engine, resolveLabels NamespaceLabelResolver) *UnifiedAuthorizer {
	return &UnifiedAuthorizer{engine: engine, resolveLabels: resolveLabels}
}

func (authorizer *UnifiedAuthorizer) Authorize(ctx context.Context, subject Subject, request Request) Decision {
	if authorizer == nil || authorizer.engine == nil {
		return Decision{}
	}
	capability := gatewayCapability(request)
	if capability == "" {
		return Decision{}
	}
	if request.Namespace != "" && !request.LabelsAvailable && authorizer.engine.UsesNamespaceSelectors() && authorizer.resolveLabels != nil {
		labels, err := authorizer.resolveLabels(ctx, request.Namespace)
		if err == nil {
			request.NamespaceLabels, request.LabelsAvailable = labels, true
		}
	}
	decision := authorizer.engine.Authorize(ctx, adminauthorization.Subject{
		ID: subject.ID, Provider: subject.Provider, Groups: append([]string(nil), subject.Groups...),
	}, adminauthorization.Request{
		Capability: capability, Namespace: request.Namespace, ResourceName: request.ResourceName,
		NamespaceLabels: cloneLabels(request.NamespaceLabels), LabelsAvailable: request.LabelsAvailable,
	})
	result := Decision{Allowed: decision.Allowed}
	if len(decision.MatchingDeny) > 0 {
		result.RuleID = decision.MatchingDeny[0].BindingID
	} else if len(decision.MatchingAllow) > 0 {
		result.RuleID = decision.MatchingAllow[0].BindingID
	}
	return result
}

func gatewayCapability(request Request) adminauthorization.Capability {
	resource, operation := strings.ToLower(strings.TrimSpace(request.ResourceKind)), strings.ToLower(strings.TrimSpace(request.Operation))
	switch resource {
	case "namespaces", "capabilities", "api", "version":
		return adminauthorization.CapabilityNamespaceAccess
	case "pods", "services":
		return "namespace.resources.read"
	case "pod-exec":
		return "namespace.exec.open"
	case "port-forwards":
		return "namespace.port-forward.open"
	case "file-transfers", "pod-files":
		if operation == "list" || operation == "get" {
			return "namespace.files.download"
		}
		return "namespace.files.upload"
	case "exchanges", "mirrors":
		if operation == "list" || operation == "get" {
			return "namespace.intercepts.read"
		}
		return "namespace.intercepts.manage"
	case "previews":
		if operation == "list" || operation == "get" {
			return "namespace.previews.read"
		}
		return "namespace.previews.manage"
	case "sessions", "tasks", "relay-tickets":
		if operation == "list" || operation == "get" || operation == "heartbeat" {
			return "namespace.tasks.read"
		}
		return "namespace.tasks.stop"
	default:
		return ""
	}
}

func cloneLabels(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
