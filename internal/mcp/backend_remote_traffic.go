package mcp

import (
	"context"
	"slices"
	"strings"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
)

func (backend *RemoteBackend) StartTraffic(ctx context.Context, request TrafficStartRequest) (TrafficItem, error) {
	serverProfile, session, err := backend.requireSession(request.ProfileID, request.SessionID, request.Namespace)
	if err != nil {
		return TrafficItem{}, err
	}
	switch request.Type {
	case trafficTypeExchange:
		if backend.dependencies.Exchanges == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Exchange is unavailable"}
		}
		targets := make([]clientexchange.LocalTarget, len(request.Targets))
		for index, target := range request.Targets {
			targets[index] = clientexchange.LocalTarget(target)
		}
		info, err := backend.dependencies.Exchanges.Start(ctx, serverProfile, session, clientexchange.Request{
			ProfileID: serverProfile.ID, Service: request.Service, Targets: targets,
		})
		return TrafficItem{Type: request.Type, Exchange: &info}, err
	case trafficTypeMirror:
		if backend.dependencies.Mirrors == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Mirror is unavailable"}
		}
		targets := make([]clientmirror.LocalTarget, len(request.Targets))
		for index, target := range request.Targets {
			targets[index] = clientmirror.LocalTarget(target)
		}
		info, err := backend.dependencies.Mirrors.Start(ctx, serverProfile, session, clientmirror.Request{
			ProfileID: serverProfile.ID, Service: request.Service, Targets: targets,
		})
		return TrafficItem{Type: request.Type, Mirror: &info}, err
	case trafficTypePreview:
		if backend.dependencies.Previews == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Preview is unavailable"}
		}
		targets := make([]clientpreview.LocalTarget, len(request.Targets))
		for index, target := range request.Targets {
			targets[index] = clientpreview.LocalTarget(target)
		}
		info, err := backend.dependencies.Previews.Start(ctx, serverProfile, session, clientpreview.Request{
			ProfileID: serverProfile.ID, Namespace: session.Namespace, Name: request.Name, Targets: targets,
		})
		return TrafficItem{Type: request.Type, Preview: &info}, err
	case trafficTypePortForward:
		if backend.dependencies.Forwards == nil {
			return TrafficItem{}, &ToolError{Code: ErrorUnavailable, Message: "Port Forward is unavailable"}
		}
		info, err := backend.dependencies.Forwards.Start(ctx, serverProfile, session, clientportforward.Request{
			ProfileID: serverProfile.ID, Kind: request.TargetKind, Name: request.TargetName,
			Protocol: request.Protocol, RemotePort: request.RemotePort, LocalPort: request.LocalPort,
		})
		return TrafficItem{Type: request.Type, PortForward: &info}, err
	default:
		return TrafficItem{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
	}
}

func (backend *RemoteBackend) PauseTraffic(ctx context.Context, identity TrafficIdentity) error {
	return backend.mutateTraffic(ctx, identity, actionPause)
}

func (backend *RemoteBackend) DeleteTraffic(ctx context.Context, identity TrafficIdentity) error {
	return backend.mutateTraffic(ctx, identity, actionDelete)
}

func (backend *RemoteBackend) mutateTraffic(ctx context.Context, identity TrafficIdentity, action string) error {
	serverProfile, _, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return err
	}
	if strings.TrimSpace(identity.TaskID) == "" {
		return invalid("taskId", "taskId is required")
	}
	switch identity.Type {
	case trafficTypeExchange:
		items := backend.dependencies.Exchanges
		if items == nil || !matchesExchange(items.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Exchange is not active"}
		}
		if action == actionPause {
			return backend.dependencies.Exchanges.Pause(ctx, serverProfile.ID, identity.TaskID)
		}
		return backend.dependencies.Exchanges.Delete(ctx, serverProfile.ID, identity.TaskID)
	case trafficTypeMirror:
		items := backend.dependencies.Mirrors
		if items == nil || !matchesMirror(items.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Mirror is not active"}
		}
		if action == actionPause {
			return backend.dependencies.Mirrors.Pause(ctx, serverProfile.ID, identity.TaskID)
		}
		return backend.dependencies.Mirrors.Delete(ctx, serverProfile.ID, identity.TaskID)
	case trafficTypePreview:
		items := backend.dependencies.Previews
		if items == nil || !matchesPreview(items.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Preview is not active"}
		}
		if action == actionPause {
			return backend.dependencies.Previews.Pause(ctx, serverProfile.ID, identity.TaskID)
		}
		return backend.dependencies.Previews.Delete(ctx, serverProfile.ID, identity.TaskID)
	case trafficTypePortForward:
		items := backend.dependencies.Forwards
		if items == nil || !matchesForward(items.List(serverProfile.ID), identity) {
			return &ToolError{Code: ErrorNotFound, Message: "Port Forward is not active"}
		}
		if action == actionPause {
			return backend.dependencies.Forwards.Pause(ctx, serverProfile.ID, identity.TaskID)
		}
		return backend.dependencies.Forwards.Delete(ctx, serverProfile.ID, identity.TaskID)
	default:
		return invalid("type", "type must be exchange, mirror, preview, or port_forward")
	}
}

func (backend *RemoteBackend) ResumeTraffic(
	ctx context.Context,
	identity TrafficIdentity,
) (TrafficItem, error) {
	serverProfile, _, err := backend.requireSession(identity.ProfileID, identity.SessionID, identity.Namespace)
	if err != nil {
		return TrafficItem{}, err
	}
	if strings.TrimSpace(identity.TaskID) == "" {
		return TrafficItem{}, invalid("taskId", "taskId is required")
	}
	switch identity.Type {
	case trafficTypeExchange:
		items := backend.dependencies.Exchanges
		if items == nil || !matchesExchange(items.List(serverProfile.ID), identity) {
			return TrafficItem{}, &ToolError{Code: ErrorNotFound, Message: "Exchange is not paused"}
		}
		info, err := items.Resume(ctx, serverProfile.ID, identity.TaskID)
		return TrafficItem{Type: identity.Type, Exchange: &info}, err
	case trafficTypeMirror:
		items := backend.dependencies.Mirrors
		if items == nil || !matchesMirror(items.List(serverProfile.ID), identity) {
			return TrafficItem{}, &ToolError{Code: ErrorNotFound, Message: "Mirror is not paused"}
		}
		info, err := items.Resume(ctx, serverProfile.ID, identity.TaskID)
		return TrafficItem{Type: identity.Type, Mirror: &info}, err
	case trafficTypePreview:
		items := backend.dependencies.Previews
		if items == nil || !matchesPreview(items.List(serverProfile.ID), identity) {
			return TrafficItem{}, &ToolError{Code: ErrorNotFound, Message: "Preview is not paused"}
		}
		info, err := items.Resume(ctx, serverProfile.ID, identity.TaskID)
		return TrafficItem{Type: identity.Type, Preview: &info}, err
	case trafficTypePortForward:
		items := backend.dependencies.Forwards
		if items == nil || !matchesForward(items.List(serverProfile.ID), identity) {
			return TrafficItem{}, &ToolError{Code: ErrorNotFound, Message: "Port Forward is not paused"}
		}
		info, err := items.Resume(ctx, serverProfile.ID, identity.TaskID)
		return TrafficItem{Type: identity.Type, PortForward: &info}, err
	default:
		return TrafficItem{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
	}
}

func (backend *RemoteBackend) ListTraffic(profileID, trafficType string) ([]TrafficItem, error) {
	serverProfile, err := backend.activeProfile(profileID)
	if err != nil {
		return nil, err
	}
	items := make([]TrafficItem, 0)
	if trafficType == "" || trafficType == trafficTypeExchange {
		if backend.dependencies.Exchanges != nil {
			for _, info := range backend.dependencies.Exchanges.List(serverProfile.ID) {
				item := info
				items = append(items, TrafficItem{Type: trafficTypeExchange, Exchange: &item})
			}
		}
	}
	if trafficType == "" || trafficType == trafficTypeMirror {
		if backend.dependencies.Mirrors != nil {
			for _, info := range backend.dependencies.Mirrors.List(serverProfile.ID) {
				item := info
				items = append(items, TrafficItem{Type: trafficTypeMirror, Mirror: &item})
			}
		}
	}
	if trafficType == "" || trafficType == trafficTypePreview {
		if backend.dependencies.Previews != nil {
			for _, info := range backend.dependencies.Previews.List(serverProfile.ID) {
				item := info
				items = append(items, TrafficItem{Type: trafficTypePreview, Preview: &item})
			}
		}
	}
	if trafficType == "" || trafficType == trafficTypePortForward {
		if backend.dependencies.Forwards != nil {
			for _, info := range backend.dependencies.Forwards.List(serverProfile.ID) {
				item := info
				items = append(items, TrafficItem{Type: trafficTypePortForward, PortForward: &item})
			}
		}
	}
	slices.SortFunc(items, func(left, right TrafficItem) int {
		return strings.Compare(trafficItemID(left), trafficItemID(right))
	})
	return items, nil
}

func matchesExchange(items []clientexchange.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func matchesMirror(items []clientmirror.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func matchesPreview(items []clientpreview.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func matchesForward(items []clientportforward.Info, identity TrafficIdentity) bool {
	for _, item := range items {
		if item.ID == identity.TaskID && item.SessionID == identity.SessionID && item.Namespace == identity.Namespace {
			return true
		}
	}
	return false
}

func trafficItemID(item TrafficItem) string {
	switch {
	case item.Exchange != nil:
		return item.Exchange.ID
	case item.Mirror != nil:
		return item.Mirror.ID
	case item.Preview != nil:
		return item.Preview.ID
	case item.PortForward != nil:
		return item.PortForward.ID
	default:
		return ""
	}
}
