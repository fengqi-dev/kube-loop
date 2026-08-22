package fileapi

import (
	"bytes"
	"context"
	"errors"
	"io"
)

type countingWriter struct {
	writer  io.Writer
	maximum uint64
	written uint64
}

func (writer *countingWriter) Write(value []byte) (int, error) {
	if writer.written > writer.maximum ||
		uint64(len(value)) > writer.maximum-writer.written {
		return 0, errors.New("download exceeds the configured size limit")
	}
	n, err := writer.writer.Write(value)
	if n > 0 {
		writer.written += uint64(n)
	}
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(value []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(value)
	}
}

type limitedBuffer struct {
	buffer  bytes.Buffer
	maximum int
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.buffer.Write(value)
	}
	return original, nil
}

func (buffer *limitedBuffer) String() string { return buffer.buffer.String() }

var _ TransferExecutor = (*KubernetesTransferExecutor)(nil)
