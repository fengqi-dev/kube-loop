package fileapi

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
)

const maximumArchiveEntries = 100_000

type Outcome struct {
	Transferred uint64
	Checksum    [32]byte
	HasChecksum bool
}

type DownloadMetadata struct{ Total uint64 }

type TransferExecutor interface {
	Upload(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		Spec,
		io.Reader,
	) (Outcome, error)
	Download(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		Spec,
		func(DownloadMetadata) error,
		io.Writer,
	) (Outcome, error)
	UploadOffset(
		context.Context,
		controlplaneapi.Identity,
		string,
		Spec,
	) (uint64, error)
}

type PodExecutor interface {
	Exec(
		context.Context,
		controlplaneapi.Identity,
		string,
		execapi.Spec,
		execapi.Streams,
	) error
}

// A resumed upload waits for the interrupted attempt's container writer to
// stop growing the partial file before it reports an offset. Two consecutive
// equal reads settle it; the bound keeps a container that writes continuously
// from stalling the request.
const (
	partialUploadSettleInterval = 50 * time.Millisecond
	partialUploadSettleReads    = 20
)

type KubernetesTransferExecutor struct {
	pods         PodExecutor
	maximumBytes uint64
}

func NewKubernetesTransferExecutor(
	pods PodExecutor,
	maximumBytes uint64,
) (*KubernetesTransferExecutor, error) {
	if pods == nil {
		return nil, errors.New("kubernetes Pod executor is required")
	}
	if maximumBytes == 0 {
		maximumBytes = defaultMaxBytes
	}
	if maximumBytes < 256<<10 || maximumBytes > 1<<40 {
		return nil, errors.New(
			"file transfer maximum size must be between 256 KiB and 1 TiB",
		)
	}
	return &KubernetesTransferExecutor{
		pods:         pods,
		maximumBytes: maximumBytes,
	}, nil
}
