package filetransfer

import (
	"context"
	"io"
	"os"

	"errors"
)

type boundedWriter struct {
	writer  io.Writer
	maximum uint64
	written uint64
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if writer.written > writer.maximum || uint64(len(value)) > writer.maximum-writer.written {
		return 0, errors.New("file transfer exceeds the configured local size limit")
	}
	n, err := writer.writer.Write(value)
	written, validCount := nonNegativeIntUint64(n)
	if !validCount {
		return 0, io.ErrShortWrite
	}
	writer.written += written
	return n, err
}

func infoSize(info os.FileInfo, err error) int64 {
	if err != nil || info == nil {
		return -1
	}
	return info.Size()
}

func nonNegativeUint64(value int64) (uint64, bool) {
	if value < 0 {
		return 0, false
	}
	return uint64(value), true
}

func nonNegativeIntUint64(value int) (uint64, bool) {
	if value < 0 {
		return 0, false
	}
	return uint64(value), true
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
