package mcp

import (
	"strings"

	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
)

func manageFileTransfer(backend Backend, input manageFileTransferIn) (manageFileTransferOut, error) {
	input.Action, input.ProfileID = strings.TrimSpace(input.Action), strings.TrimSpace(input.ProfileID)
	if input.ProfileID == "" {
		return manageFileTransferOut{}, invalid("profileId", "profileId is required")
	}
	switch input.Action {
	case actionList:
		items, err := backend.ListFileTransfers(input.ProfileID)
		return manageFileTransferOut{Action: input.Action, Items: items}, err
	case actionStart:
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageFileTransferOut{}, err
		}
		if input.Direction != "upload" && input.Direction != "download" {
			return manageFileTransferOut{}, invalid("direction", "direction must be upload or download")
		}
		if input.Kind != fileKindFile && input.Kind != fileKindDirectory {
			return manageFileTransferOut{}, invalid("kind", "kind must be file or directory")
		}
		if strings.TrimSpace(input.Pod) == "" {
			return manageFileTransferOut{}, invalid(resourcePod, "pod is required")
		}
		if strings.TrimSpace(input.LocalPath) == "" {
			return manageFileTransferOut{}, invalid("localPath", "localPath is required")
		}
		if strings.TrimSpace(input.RemotePath) == "" {
			return manageFileTransferOut{}, invalid("remotePath", "remotePath is required")
		}
		task, err := backend.StartFileTransfer(TrafficIdentity{
			ProfileID: input.ProfileID, SessionID: input.SessionID, Namespace: input.Namespace,
		}, clientfiletransfer.Request{
			ProfileID: input.ProfileID, Direction: input.Direction, Kind: input.Kind,
			Pod: input.Pod, Container: input.Container, LocalPath: input.LocalPath,
			RemotePath: input.RemotePath, Overwrite: input.Overwrite,
		})
		return manageFileTransferOut{Action: input.Action, TaskID: task.ID, Task: &task}, err
	case "cancel":
		if err := validateMutationIdentity(input.ProfileID, input.SessionID, input.Namespace); err != nil {
			return manageFileTransferOut{}, err
		}
		if strings.TrimSpace(input.TaskID) == "" {
			return manageFileTransferOut{}, invalid("taskId", "taskId is required")
		}
		err := backend.CancelFileTransfer(TrafficIdentity{
			ProfileID: input.ProfileID, SessionID: input.SessionID,
			Namespace: input.Namespace, TaskID: input.TaskID,
		})
		return manageFileTransferOut{Action: input.Action, TaskID: input.TaskID}, err
	default:
		return manageFileTransferOut{}, invalid("action", "action must be start, list, or cancel")
	}
}
