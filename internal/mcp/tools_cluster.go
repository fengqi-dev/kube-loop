package mcp

import (
	"context"
	"strings"
)

func manageCluster(ctx context.Context, backend Backend, input manageClusterIn) (manageClusterOut, error) {
	input.Action, input.Type = strings.TrimSpace(input.Action), strings.TrimSpace(input.Type)
	output := manageClusterOut{
		Action: input.Action, Type: input.Type, ProfileID: strings.TrimSpace(input.ProfileID),
		Namespace: strings.TrimSpace(input.Namespace),
	}
	if output.ProfileID == "" {
		return manageClusterOut{}, invalid(fieldProfileID, "profileId is required")
	}
	switch {
	case input.Action == "get" && input.Type == "version":
		value, err := backend.Version(ctx, output.ProfileID)
		output.Version = &value
		return output, err
	case input.Action == "get" && input.Type == "capabilities":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid(resourceNamespace, "namespace is required for capabilities")
		}
		value, err := backend.Capabilities(ctx, output.ProfileID, output.Namespace)
		output.Capabilities = &value
		return output, err
	case input.Action == actionList && input.Type == resourceNamespace:
		items, err := backend.Namespaces(ctx, output.ProfileID)
		output.Namespaces = items
		return output, err
	case input.Action == actionList && input.Type == "service":
		if output.Namespace == "" {
			return manageClusterOut{}, invalid(resourceNamespace, "namespace is required for Services")
		}
		items, err := backend.Services(ctx, output.ProfileID, output.Namespace)
		output.Services = items
		return output, err
	case input.Action == actionList && input.Type == resourcePod:
		if output.Namespace == "" {
			return manageClusterOut{}, invalid(resourceNamespace, "namespace is required for Pods")
		}
		items, err := backend.Pods(ctx, output.ProfileID, output.Namespace)
		output.Pods = items
		return output, err
	default:
		return manageClusterOut{}, invalid(
			"action",
			"supported combinations are get/version, get/capabilities, and list/namespace|service|pod",
		)
	}
}
