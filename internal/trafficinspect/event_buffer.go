package trafficinspect

import "sync"

// EventBuffer keeps the newest inspection events for the desktop API.
type EventBuffer struct {
	mu       sync.RWMutex
	capacity int
	events   []Event
}

func NewEventBuffer(capacity int) *EventBuffer {
	if capacity < 1 {
		return nil
	}
	return &EventBuffer{capacity: capacity}
}

func (buffer *EventBuffer) Append(event Event) {
	if buffer == nil {
		return
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.events = append(buffer.events, cloneEvent(event))
	if len(buffer.events) > buffer.capacity {
		buffer.events = buffer.events[len(buffer.events)-buffer.capacity:]
	}
}

func (buffer *EventBuffer) Snapshot() []Event {
	if buffer == nil {
		return nil
	}
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()
	result := make([]Event, len(buffer.events))
	for index, event := range buffer.events {
		result[index] = cloneEvent(event)
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
	if event.Protobuf != nil {
		protobufEvent := *event.Protobuf
		event.Protobuf = &protobufEvent
	}
	if event.Raw != nil {
		rawEvent := *event.Raw
		event.Raw = &rawEvent
	}
	return event
}
