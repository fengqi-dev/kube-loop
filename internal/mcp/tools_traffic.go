package mcp

import (
	"context"
	"strings"
)

func manageTraffic(ctx context.Context, backend Backend, input manageTrafficIn) (manageTrafficOut, error) {
	input.Action, input.Type = strings.TrimSpace(input.Action), strings.TrimSpace(input.Type)
	input.ProfileID = strings.TrimSpace(input.ProfileID)
	if input.ProfileID == "" {
		return manageTrafficOut{}, invalid("profileId", "profileId is required")
	}
	switch input.Action {
	case actionList:
		if input.Type != "" && !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		items, err := backend.ListTraffic(input.ProfileID, input.Type)
		return manageTrafficOut{Action: input.Action, Type: input.Type, Items: items}, err
	case actionStart:
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageTrafficOut{}, err
		}
		if !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		switch input.Type {
		case trafficTypeExchange, trafficTypeMirror:
			if strings.TrimSpace(input.Service) == "" {
				return manageTrafficOut{}, invalid("service", "service is required")
			}
			if len(input.Targets) == 0 {
				return manageTrafficOut{}, invalid("targets", "at least one explicit local target is required")
			}
		case trafficTypePreview:
			if strings.TrimSpace(input.Name) == "" {
				return manageTrafficOut{}, invalid("name", "name is required")
			}
			if len(input.Targets) == 0 {
				return manageTrafficOut{}, invalid("targets", "at least one explicit local target is required")
			}
		default:
			if input.TargetKind != resourcePod && input.TargetKind != "service" {
				return manageTrafficOut{}, invalid("targetKind", "targetKind must be pod or service")
			}
			if strings.TrimSpace(input.TargetName) == "" {
				return manageTrafficOut{}, invalid("targetName", "targetName is required")
			}
			if input.RemotePort == 0 {
				return manageTrafficOut{}, invalid("remotePort", "remotePort is required")
			}
		}
		targets := make([]LocalTarget, len(input.Targets))
		for index, target := range input.Targets {
			targets[index] = LocalTarget(target)
		}
		item, err := backend.StartTraffic(ctx, TrafficStartRequest{
			Type:       input.Type,
			ProfileID:  input.ProfileID,
			SessionID:  input.SessionID,
			Namespace:  input.Namespace,
			Service:    input.Service,
			Name:       input.Name,
			Targets:    targets,
			TargetKind: input.TargetKind,
			TargetName: input.TargetName,
			Protocol:   input.Protocol,
			RemotePort: input.RemotePort,
			LocalPort:  input.LocalPort,
		})
		return manageTrafficOut{Action: input.Action, Type: input.Type, TaskID: trafficItemID(item), Item: &item}, err
	case actionPause, actionResume, actionDelete:
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageTrafficOut{}, err
		}
		if !validTrafficType(input.Type) {
			return manageTrafficOut{}, invalid("type", "type must be exchange, mirror, preview, or port_forward")
		}
		if strings.TrimSpace(input.TaskID) == "" {
			return manageTrafficOut{}, invalid("taskId", "taskId is required")
		}
		identity := TrafficIdentity{
			Type: input.Type, ProfileID: input.ProfileID, SessionID: input.SessionID,
			Namespace: input.Namespace, TaskID: input.TaskID,
		}
		switch input.Action {
		case actionPause:
			err := backend.PauseTraffic(ctx, identity)
			return manageTrafficOut{Action: input.Action, Type: input.Type, TaskID: input.TaskID}, err
		case actionResume:
			item, err := backend.ResumeTraffic(ctx, identity)
			return manageTrafficOut{
				Action: input.Action, Type: input.Type, TaskID: input.TaskID, Item: &item,
			}, err
		default:
			err := backend.DeleteTraffic(ctx, identity)
			return manageTrafficOut{Action: input.Action, Type: input.Type, TaskID: input.TaskID}, err
		}
	default:
		return manageTrafficOut{}, invalid("action", "action must be start, pause, resume, delete, or list")
	}
}
