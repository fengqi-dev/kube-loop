package mcp

import (
	"context"
	"strings"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func managePodFiles(ctx context.Context, backend Backend, input managePodFilesIn) (managePodFilesOut, error) {
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	identity := TrafficIdentity{
		ProfileID: strings.TrimSpace(input.ProfileID),
		SessionID: strings.TrimSpace(input.SessionID),
		Namespace: strings.TrimSpace(input.Namespace),
	}
	if err := validateMutationIdentity(identity.ProfileID, identity.SessionID, identity.Namespace); err != nil {
		return managePodFilesOut{}, err
	}
	input.Pod = strings.TrimSpace(input.Pod)
	input.Container = strings.TrimSpace(input.Container)
	input.Path = strings.TrimSpace(input.Path)
	if input.Pod == "" {
		return managePodFilesOut{}, invalid(resourcePod, "pod is required")
	}
	if input.Path == "" {
		return managePodFilesOut{}, invalid("path", "path is required")
	}
	output := managePodFilesOut{
		Action: input.Action, ProfileID: identity.ProfileID,
		SessionID: identity.SessionID, Namespace: identity.Namespace,
	}
	spec := clientremote.PodFileSpec{
		Pod: input.Pod, Container: input.Container, Path: input.Path,
		Destination: strings.TrimSpace(input.Destination),
		Kind:        strings.ToLower(strings.TrimSpace(input.Kind)), Recursive: input.Recursive,
	}
	if input.Action == actionList {
		listing, err := backend.ListPodFiles(ctx, identity, spec)
		output.Listing = &listing
		return output, err
	}
	if input.Action != actionCreate && input.Action != actionRename && input.Action != actionDelete {
		return managePodFilesOut{}, invalid("action", "action must be list, create, rename, or delete")
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return managePodFilesOut{}, invalid(
			"idempotencyKey",
			"idempotencyKey is required and must be at most 128 bytes",
		)
	}
	if input.Action == actionCreate && spec.Kind != fileKindFile && spec.Kind != fileKindDirectory {
		return managePodFilesOut{}, invalid("kind", "kind must be file or directory for create")
	}
	if input.Action == actionRename && spec.Destination == "" {
		return managePodFilesOut{}, invalid("destination", "destination is required for rename")
	}
	task, err := backend.CreatePodFileOperation(ctx, identity, input.Action, spec, input.IdempotencyKey)
	output.Task = &task
	return output, err
}
