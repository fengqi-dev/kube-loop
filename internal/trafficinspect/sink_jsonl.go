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
	"time"
)

type JSONLSink struct {
	access  sync.Mutex
	encoder *json.Encoder
	closer  io.Closer
}

type DailyJSONLFileSink struct {
	access  sync.Mutex
	path    string
	root    *os.Root
	name    string
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
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return nil, fmt.Errorf("trafficinspect: open jsonl file directory: %w", err)
	}
	sink := &DailyJSONLFileSink{path: absolute, root: root, name: filepath.Base(absolute), now: now}
	if err := sink.open(now()); err != nil {
		_ = root.Close()
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
	var fileErr error
	if s.file != nil {
		fileErr = s.file.Close()
		s.file = nil
		s.encoder = nil
	}
	var rootErr error
	if s.root != nil {
		rootErr = s.root.Close()
		s.root = nil
	}
	return errors.Join(fileErr, rootErr)
}

func (s *DailyJSONLFileSink) open(now time.Time) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := s.root.Stat(s.name); err == nil && dayKey(info.ModTime()) != dayKey(now) {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("trafficinspect: stat daily jsonl file: %w", err)
	}
	file, err := s.root.OpenFile(s.name, flags, 0o600)
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
	file, err := s.root.OpenFile(s.name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
