package fileapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type streamResult struct {
	Transferred uint64 `json:"transferred"`
	Checksum    string `json:"checksum,omitempty"`
	Cancelled   bool   `json:"cancelled,omitempty"`
	Error       string `json:"error,omitempty"`
}

func resultFromOutcome(
	outcome Outcome,
	err error,
	cancelled bool,
) streamResult {
	result := streamResult{
		Transferred: outcome.Transferred,
		Cancelled:   cancelled,
	}
	if outcome.HasChecksum {
		result.Checksum = filestream.FormatChecksum(outcome.Checksum)
	}
	if err != nil && !cancelled {
		result.Error = "file transfer failed"
	}
	return result
}

func (result streamResult) protocol() filestream.TransferResult {
	status := filestream.ResultSucceeded
	if result.Cancelled {
		status = filestream.ResultCancelled
	} else if result.Error != "" {
		status = filestream.ResultFailed
	}
	protocol := filestream.TransferResult{
		Status: status, Transferred: result.Transferred, HasChecksum: result.Checksum != "", Error: result.Error,
	}
	if protocol.HasChecksum {
		protocol.Checksum, _ = filestream.ParseChecksum(result.Checksum)
	}
	return protocol
}

func (handler *Service) persistState(
	parent context.Context,
	taskID string,
	expected, next remotetask.State,
	result streamResult,
) error {
	encoded, _ := json.Marshal(result)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	return handler.storage.Tasks().
		UpdateState(ctx, taskID, expected, next, encoded, handler.now().UTC())
}
