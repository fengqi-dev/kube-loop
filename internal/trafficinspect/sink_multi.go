package trafficinspect

import (
	"context"
	"errors"
	"io"
)

type MultiSink struct {
	sinks []Sink
}

func NewMultiSink(sinks ...Sink) (*MultiSink, error) {
	result := &MultiSink{sinks: make([]Sink, 0, len(sinks))}
	for _, sink := range sinks {
		if sink != nil {
			result.sinks = append(result.sinks, sink)
		}
	}
	if len(result.sinks) == 0 {
		return nil, errors.New("trafficinspect: at least one sink is required")
	}
	return result, nil
}

func (s *MultiSink) Emit(ctx context.Context, event Event) error {
	var result error
	for _, sink := range s.sinks {
		result = errors.Join(result, sink.Emit(ctx, event))
	}
	return result
}

func (s *MultiSink) Close() error {
	var result error
	for _, sink := range s.sinks {
		if closer, ok := sink.(io.Closer); ok {
			result = errors.Join(result, closer.Close())
		}
	}
	return result
}
