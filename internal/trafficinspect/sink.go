package trafficinspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

type JSONLSink struct {
	access  sync.Mutex
	encoder *json.Encoder
	closer  io.Closer
}

type DailyJSONLFileSink struct {
	access  sync.Mutex
	path    string
	day     string
	encoder *json.Encoder
	file    *os.File
	now     func() time.Time
}

func NewDailyJSONLFileSink(path string) (*DailyJSONLFileSink, error) {
	return newDailyJSONLFileSink(path, time.Now)
}

func newDailyJSONLFileSink(path string, now func() time.Time) (*DailyJSONLFileSink, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("trafficinspect: jsonl file path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("trafficinspect: resolve jsonl file path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("trafficinspect: create jsonl file directory: %w", err)
	}
	sink := &DailyJSONLFileSink{path: absolute, now: now}
	if err := sink.open(now()); err != nil {
		return nil, err
	}
	return sink, nil
}

func (s *DailyJSONLFileSink) Emit(_ context.Context, event Event) error {
	s.access.Lock()
	defer s.access.Unlock()
	now := s.now()
	if s.day != dayKey(now) {
		if err := s.rotate(now); err != nil {
			return err
		}
	}
	return s.encoder.Encode(event)
}

func (s *DailyJSONLFileSink) Close() error {
	s.access.Lock()
	defer s.access.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	s.encoder = nil
	return err
}

func (s *DailyJSONLFileSink) open(now time.Time) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(s.path); err == nil && dayKey(info.ModTime()) != dayKey(now) {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("trafficinspect: stat daily jsonl file: %w", err)
	}
	file, err := os.OpenFile(s.path, flags, 0o600)
	if err != nil {
		return fmt.Errorf("trafficinspect: open daily jsonl file: %w", err)
	}
	s.file = file
	s.encoder = json.NewEncoder(file)
	s.day = dayKey(now)
	return nil
}

func (s *DailyJSONLFileSink) rotate(now time.Time) error {
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			return fmt.Errorf("trafficinspect: close previous daily jsonl file: %w", err)
		}
		s.file = nil
		s.encoder = nil
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("trafficinspect: rotate daily jsonl file: %w", err)
	}
	s.file = file
	s.encoder = json.NewEncoder(file)
	s.day = dayKey(now)
	return nil
}

func dayKey(value time.Time) string {
	return value.Format("2006-01-02")
}

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

func NewJSONLSink(destination io.Writer) *JSONLSink {
	if destination == nil {
		return &JSONLSink{}
	}
	return &JSONLSink{encoder: json.NewEncoder(destination)}
}

func NewJSONLFileSink(path string) (*JSONLSink, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("trafficinspect: jsonl file path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("trafficinspect: resolve jsonl file path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("trafficinspect: create jsonl file directory: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trafficinspect: open jsonl file: %w", err)
	}
	return &JSONLSink{encoder: json.NewEncoder(file), closer: file}, nil
}

func (s *JSONLSink) Emit(_ context.Context, event Event) error {
	s.access.Lock()
	defer s.access.Unlock()
	if s.encoder == nil {
		return errors.New("trafficinspect: jsonl destination is required")
	}
	return s.encoder.Encode(event)
}

func (s *JSONLSink) Close() error {
	s.access.Lock()
	defer s.access.Unlock()
	s.encoder = nil
	if s.closer == nil {
		return nil
	}
	err := s.closer.Close()
	s.closer = nil
	return err
}

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
