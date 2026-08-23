package trafficinspect

import (
	"context"
	"errors"
	"sync"
)

type ChannelSink struct {
	events chan Event
}

func NewChannelSink(capacity int) (*ChannelSink, error) {
	if capacity < 1 {
		return nil, errors.New("trafficinspect: channel sink capacity must be positive")
	}
	return &ChannelSink{events: make(chan Event, capacity)}, nil
}

func (s *ChannelSink) Events() <-chan Event {
	return s.events
}

func (s *ChannelSink) Emit(ctx context.Context, event Event) error {
	select {
	case s.events <- cloneEvent(event):
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	default:
		return ErrSinkFull
	}
}

type RingBufferSink struct {
	access sync.RWMutex
	events []Event
	next   int
	full   bool
}

func NewRingBufferSink(capacity int) (*RingBufferSink, error) {
	if capacity < 1 {
		return nil, errors.New("trafficinspect: ring buffer sink capacity must be positive")
	}
	return &RingBufferSink{events: make([]Event, capacity)}, nil
}

func (s *RingBufferSink) Emit(_ context.Context, event Event) error {
	s.access.Lock()
	defer s.access.Unlock()
	s.events[s.next] = cloneEvent(event)
	s.next = (s.next + 1) % len(s.events)
	if s.next == 0 {
		s.full = true
	}
	return nil
}

func (s *RingBufferSink) Snapshot() []Event {
	s.access.RLock()
	defer s.access.RUnlock()
	count := s.next
	start := 0
	if s.full {
		count = len(s.events)
		start = s.next
	}
	result := make([]Event, 0, count)
	for offset := range count {
		index := (start + offset) % len(s.events)
		result = append(result, cloneEvent(s.events[index]))
	}
	return result
}

func cloneEvent(event Event) Event {
	if event.HTTP != nil {
		httpEvent := *event.HTTP
		httpEvent.RequestHeaders = event.HTTP.RequestHeaders.Clone()
		httpEvent.ResponseHeaders = event.HTTP.ResponseHeaders.Clone()
		event.HTTP = &httpEvent
	}
	if event.GRPC != nil {
		grpcEvent := *event.GRPC
		event.GRPC = &grpcEvent
	}
	if event.Raw != nil {
		raw := *event.Raw
		event.Raw = &raw
	}
	return event
}
