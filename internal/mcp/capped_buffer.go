package mcp

import "io"

type cappedBuffer struct {
	value     []byte
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer { return &cappedBuffer{limit: limit} }

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := buffer.limit - len(buffer.value)
	if remaining > 0 {
		buffer.value = append(buffer.value, value[:min(remaining, len(value))]...)
	}
	if len(value) > remaining {
		buffer.truncated = true
	}
	return written, nil
}

func (buffer *cappedBuffer) Bytes() []byte { return append([]byte(nil), buffer.value...) }

var _ io.Writer = (*cappedBuffer)(nil)
