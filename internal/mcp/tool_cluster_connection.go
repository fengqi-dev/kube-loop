package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

func manageCluster(
	ctx context.Context,
	backend Backend,
	in manageClusterIn,
) (manageClusterOut, error) {
	out := manageClusterOut{Action: in.Action, Type: in.Type}
	switch {
	case in.Action == "reload" && in.Type == "context":
		inventory, err := backend.ReloadContexts()
		if err != nil {
			return manageClusterOut{}, err
		}
		out.Inventory = &inventory
	case in.Action == "probe" && in.Type == "context":
		if in.Context == "" {
			return manageClusterOut{}, fmt.Errorf("context is required when probing")
		}
		probe, err := backend.ProbeContext(ctx, in.Context)
		if err != nil {
			return manageClusterOut{}, err
		}
		out.Probe = &probe
	case in.Action == "list" && in.Type == "namespace":
		if in.Context == "" {
			return manageClusterOut{}, fmt.Errorf("context is required when listing namespaces")
		}
		items, err := backend.Namespaces(ctx, in.Context)
		if err != nil {
			return manageClusterOut{}, err
		}
		out.Namespaces = items
	case in.Action == "list" && (in.Type == "service" || in.Type == "pod"):
		if in.Context == "" {
			return manageClusterOut{}, fmt.Errorf("context is required when listing %ss", in.Type)
		}
		if in.Namespace == "" {
			return manageClusterOut{}, fmt.Errorf("namespace is required when listing %ss", in.Type)
		}
		if in.Type == "service" {
			items, err := backend.ListServices(ctx, in.Context, in.Namespace)
			if err != nil {
				return manageClusterOut{}, err
			}
			out.Services = items
		} else {
			items, err := backend.ListPods(ctx, in.Context, in.Namespace)
			if err != nil {
				return manageClusterOut{}, err
			}
			out.Pods = items
		}
	default:
		return manageClusterOut{}, fmt.Errorf(
			"supported combinations are reload/context, probe/context, and list/namespace|service|pod",
		)
	}
	return out, nil
}

func manageConnection(
	ctx context.Context,
	backend Backend,
	in manageConnectionIn,
) (manageConnectionOut, error) {
	out := manageConnectionOut{Action: in.Action}
	switch in.Action {
	case "status":
	case "connect":
		if in.Context == "" {
			return manageConnectionOut{}, fmt.Errorf("context is required when connecting")
		}
		if err := backend.Connect(ctx, in.Context, in.Namespace); err != nil {
			return manageConnectionOut{}, err
		}
	case "disconnect":
		if err := backend.Disconnect(); err != nil {
			return manageConnectionOut{}, err
		}
	case "config":
		raw, err := backend.SingBoxConfig()
		if err != nil {
			return manageConnectionOut{}, err
		}
		out.Config = json.RawMessage(raw)
	default:
		return manageConnectionOut{}, fmt.Errorf(
			"action must be status, connect, disconnect, or config",
		)
	}
	state := backend.SessionState()
	out.State = &state
	return out, nil
}
