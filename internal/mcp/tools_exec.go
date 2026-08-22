package mcp

import (
	"context"
	"strings"
)

func execPodCommand(ctx context.Context, backend Backend, input podCommandIn) (PodCommandResult, error) {
	if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
		return PodCommandResult{}, err
	}
	if strings.TrimSpace(input.Pod) == "" {
		return PodCommandResult{}, invalid(resourcePod, "pod is required")
	}
	if len(input.Command) == 0 {
		return PodCommandResult{}, invalid("command", "command must contain explicit argv")
	}
	return backend.ExecPodCommand(ctx, PodCommandRequest{
		ProfileID: input.ProfileID, SessionID: input.SessionID, Namespace: input.Namespace,
		Pod: input.Pod, Container: input.Container, Command: append([]string(nil), input.Command...),
		TimeoutSeconds: input.TimeoutSeconds,
	})
}
