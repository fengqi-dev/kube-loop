package supervisor

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func WriteFrame(w io.Writer, value any, maximum uint32) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if len(raw) == 0 || uint64(len(raw)) > uint64(maximum) {
		return fmt.Errorf("frame size %d is outside 1..%d", len(raw), maximum)
	}
	var header [4]byte
	// The maximum uint32 frame size check above makes this conversion safe.
	binary.BigEndian.PutUint32(header[:], uint32(len(raw))) //nolint:gosec // Checked against uint32 maximum.
	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

func ReadFrame(r io.Reader, value any, maximum uint32) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("read frame header: %w", err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maximum {
		return fmt.Errorf("frame size %d is outside 1..%d", size, maximum)
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(r, raw); err != nil {
		return fmt.Errorf("read frame body: %w", err)
	}
	decoder := json.NewDecoder(newSliceReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode frame: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode frame: trailing JSON value")
		}
		return fmt.Errorf("decode frame trailing data: %w", err)
	}
	return nil
}

type sliceReader struct {
	raw []byte
	off int
}

func newSliceReader(raw []byte) *sliceReader { return &sliceReader{raw: raw} }

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.off == len(r.raw) {
		return 0, io.EOF
	}
	n := copy(p, r.raw[r.off:])
	r.off += n
	return n, nil
}
