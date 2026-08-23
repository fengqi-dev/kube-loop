package powerwatch

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatcherDetectsOneWakeGapAndStops(t *testing.T) {
	base := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	times := make(chan time.Time, 4)
	events := make(chan Event, 2)
	var current atomic.Int64
	current.Store(base.UnixNano())
	initialized := make(chan struct{})
	var initialize sync.Once
	watcher, err := New(Config{
		Interval: time.Second, WakeGap: 5 * time.Second,
		OnWake: func(event Event) { events <- event },
		now: func() time.Time {
			initialize.Do(func() { close(initialized) })
			return time.Unix(0, current.Load()).UTC()
		},
		ticks: times,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()
	<-initialized
	current.Store(base.Add(time.Second).UnixNano())
	times <- base.Add(time.Second)
	select {
	case event := <-events:
		t.Fatalf("normal scheduler tick emitted wake: %#v", event)
	case <-time.After(20 * time.Millisecond):
	}
	wakeTime := base.Add(11 * time.Second)
	current.Store(wakeTime.UnixNano())
	times <- wakeTime
	select {
	case event := <-events:
		if event.SleptFor != 10*time.Second || !event.DetectedAt.Equal(wakeTime) {
			t.Fatalf("wake event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("wake gap was not detected")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestWatcherRejectsUnsafeTiming(t *testing.T) {
	for _, config := range []Config{
		{Interval: time.Second, WakeGap: time.Second, OnWake: func(Event) {}},
		{Interval: time.Second, WakeGap: 5 * time.Second},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("accepted invalid config: %#v", config)
		}
	}
}

func TestWatcherStopsWhenInjectedTicksClose(t *testing.T) {
	ticks := make(chan time.Time)
	watcher, err := New(Config{
		Interval: time.Second,
		WakeGap:  5 * time.Second,
		OnWake:   func(Event) {},
		ticks:    ticks,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		watcher.Run(t.Context())
		close(done)
	}()
	close(ticks)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after injected ticks closed")
	}
}
