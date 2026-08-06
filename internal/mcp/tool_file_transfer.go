package mcp

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/filemanager"
)

func manageFileTransfer(
	ctx context.Context,
	backend Backend,
	in manageFileTransferIn,
) (manageFileTransferOut, error) {
	out := manageFileTransferOut{Action: in.Action}
	switch in.Action {
	case "start":
		task, err := backend.StartFileTransfer(ctx, filemanager.TransferRequest{
			Direction: in.Direction,
			Target: filemanager.Target{
				Context: in.Context, Namespace: in.Namespace, Pod: in.Pod,
				PodUID: in.PodUID, Container: in.Container,
			},
			SourcePath: in.SourcePath, DestinationDir: in.DestinationDir,
			Overwrite: in.Overwrite,
		})
		if err != nil {
			return manageFileTransferOut{}, err
		}
		out.ID = task.ID
		out.Task = &task
	case "list":
		out.Items = backend.ListFileTransfers()
	case "cancel":
		if in.ID == "" {
			return manageFileTransferOut{}, fmt.Errorf("id is required when cancelling a file transfer")
		}
		if err := backend.CancelFileTransfer(in.ID); err != nil {
			return manageFileTransferOut{}, err
		}
		out.ID = in.ID
	default:
		return manageFileTransferOut{}, fmt.Errorf("action must be start, list, or cancel")
	}
	return out, nil
}
