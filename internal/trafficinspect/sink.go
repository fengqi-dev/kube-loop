package trafficinspect

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
)

var ErrSinkFull = errors.New("trafficinspect: sink is full")

// SwitchableSink gates a sink without replacing it. It is safe to toggle while
// active proxy connections are concurrently emitting events.
type SwitchableSink struct {
	enabled atomic.Bool
	sink    Sink
}

func NewSwitchableSink(sink Sink, enabled bool) (*SwitchableSink, error) {
	if sink == nil {
		return nil, errors.New("trafficinspect: switchable sink destination is required")
	}
	result := &SwitchableSink{sink: sink}
	result.enabled.Store(enabled)
	return result, nil
}

func (s *SwitchableSink) Enabled() bool {
	return s != nil && s.enabled.Load()
}

func (s *SwitchableSink) SetEnabled(enabled bool) {
	if s != nil {
		s.enabled.Store(enabled)
	}
}

func (s *SwitchableSink) Emit(ctx context.Context, event Event) error {
	if !s.Enabled() {
		return nil
	}
	return s.sink.Emit(ctx, event)
}

func (s *SwitchableSink) Close() error {
	if s == nil {
		return nil
	}
	if closer, ok := s.sink.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}
