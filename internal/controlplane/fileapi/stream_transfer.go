package fileapi

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func (handler *Service) upload(
	leaseContext context.Context,
	connection *websocket.Conn,
	writeMu *sync.Mutex,
	identity controlplaneapi.Identity,
	namespace, taskID string,
	spec Spec,
) (Outcome, error) {
	transferContext, cancelTransfer := context.WithCancel(leaseContext)
	defer cancelTransfer()
	reader, writer := io.Pipe()
	inputResult := make(chan error, 1)
	go func() {
		err := readUpload(transferContext, connection, writer, writeMu, spec)
		if err != nil {
			cancelTransfer()
		}
		inputResult <- err
	}()
	outcome, transferErr := handler.executor.Upload(
		transferContext,
		identity,
		namespace,
		taskID,
		spec,
		reader,
	)
	_ = reader.CloseWithError(transferErr)
	inputErr := <-inputResult
	if transferErr != nil {
		return outcome, transferErr
	}
	return outcome, inputErr
}

func (handler *Service) download(
	leaseContext, socketContext context.Context,
	cancel context.CancelFunc,
	connection *websocket.Conn,
	writeMu *sync.Mutex,
	readers *sync.WaitGroup,
	identity controlplaneapi.Identity,
	namespace, taskID string,
	spec Spec,
) (Outcome, error) {
	readers.Go(func() {
		readDownloadControl(socketContext, connection, cancel)
	})
	output := &downloadWriter{
		ctx: leaseContext, connection: connection, mu: writeMu,
		transferred: spec.Offset, maximum: handler.maximumBytes,
	}
	outcome, err := handler.executor.Download(
		leaseContext, identity, namespace, taskID, spec,
		func(metadata DownloadMetadata) error {
			output.total = metadata.Total
			return output.progress()
		},
		output,
	)
	if err != nil {
		return outcome, err
	}
	if outcome.Transferred != output.transferred {
		return outcome, errors.New(
			"file transfer byte count does not match the streamed output",
		)
	}
	return outcome, nil
}
