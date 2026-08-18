package trafficinspect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJSONLSink(t *testing.T) {
	var output bytes.Buffer
	sink := NewJSONLSink(&output)
	event := Event{SchemaVersion: EventSchemaVersion, ID: "event-1", Type: EventTypeRequest}
	if err := sink.Emit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	var decoded Event
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != event.ID || decoded.SchemaVersion != EventSchemaVersion {
		t.Fatalf("decoded event = %#v", decoded)
	}
}

func TestJSONLFileSinkAppendsEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inspection", "traffic.jsonl")
	for _, id := range []string{"event-1", "event-2"} {
		sink, err := NewJSONLFileSink(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := sink.Emit(t.Context(), Event{SchemaVersion: EventSchemaVersion, ID: id}); err != nil {
			t.Fatal(err)
		}
		if err := sink.Close(); err != nil {
			t.Fatal(err)
		}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	decoder := json.NewDecoder(bytes.NewReader(contents))
	for decoder.More() {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, event.ID)
	}
	if len(ids) != 2 || ids[0] != "event-1" || ids[1] != "event-2" {
		t.Fatalf("event IDs = %v", ids)
	}
}

func TestJSONLFileSinkRejectsEmptyPath(t *testing.T) {
	if _, err := NewJSONLFileSink("  "); err == nil {
		t.Fatal("expected empty path error")
	}
}

func TestDailyJSONLFileSinkKeepsOnlyCurrentDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.jsonl")
	current := time.Date(2026, time.August, 18, 23, 59, 0, 0, time.Local)
	sink, err := newDailyJSONLFileSink(path, func() time.Time { return current })
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), Event{ID: "yesterday"}); err != nil {
		t.Fatal(err)
	}
	current = current.Add(2 * time.Minute)
	if err := sink.Emit(t.Context(), Event{ID: "today"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(contents, &event); err != nil {
		t.Fatal(err)
	}
	if event.ID != "today" {
		t.Fatalf("retained event = %q", event.ID)
	}
}

func TestDailyJSONLFileSinkDropsStaleFileOnStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.jsonl")
	if err := os.WriteFile(path, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.Local)
	yesterday := current.Add(-24 * time.Hour)
	if err := os.Chtimes(path, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	sink, err := newDailyJSONLFileSink(path, func() time.Time { return current })
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), Event{ID: "fresh"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(contents, &event); err != nil {
		t.Fatal(err)
	}
	if event.ID != "fresh" {
		t.Fatalf("retained event = %q", event.ID)
	}
}

func TestChannelSinkRejectsOverflow(t *testing.T) {
	sink, err := NewChannelSink(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), Event{ID: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), Event{ID: "second"}); !errors.Is(err, ErrSinkFull) {
		t.Fatalf("overflow error = %v, want %v", err, ErrSinkFull)
	}
	if event := <-sink.Events(); event.ID != "first" {
		t.Fatalf("event id = %q", event.ID)
	}
}

func TestChannelSinkHonorsCancellation(t *testing.T) {
	sink, err := NewChannelSink(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), Event{ID: "occupied"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sink.Emit(ctx, Event{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled emit = %v", err)
	}
}

func TestRingBufferSinkKeepsNewestEvents(t *testing.T) {
	sink, err := NewRingBufferSink(2)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		if err := sink.Emit(t.Context(), Event{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	events := sink.Snapshot()
	if len(events) != 2 || events[0].ID != "two" || events[1].ID != "three" {
		t.Fatalf("snapshot = %#v", events)
	}
}

func TestSwitchableSinkAppliesChangesImmediately(t *testing.T) {
	destination, err := NewRingBufferSink(4)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewSwitchableSink(destination, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), Event{ID: "disabled"}); err != nil {
		t.Fatal(err)
	}
	sink.SetEnabled(true)
	if err := sink.Emit(t.Context(), Event{ID: "enabled"}); err != nil {
		t.Fatal(err)
	}
	sink.SetEnabled(false)
	if err := sink.Emit(t.Context(), Event{ID: "disabled-again"}); err != nil {
		t.Fatal(err)
	}
	events := destination.Snapshot()
	if len(events) != 1 || events[0].ID != "enabled" {
		t.Fatalf("events = %#v", events)
	}
}
