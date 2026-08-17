// Package powerwatch detects a host wake by observing a long wall-clock gap
// between scheduler ticks. The implementation is deliberately portable: it
// requires no platform daemon, cgo callback, or desktop framework event and
// therefore behaves identically on macOS, Windows, and Linux.
package powerwatch

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	DefaultInterval = 5 * time.Second
	DefaultWakeGap  = 20 * time.Second
)

type Event struct {
	SleptFor   time.Duration
	DetectedAt time.Time
}

type Config struct {
	Interval time.Duration
	WakeGap  time.Duration
	OnWake   func(Event)

	now   func() time.Time
	ticks <-chan time.Time
}

type Watcher struct {
	interval time.Duration
	wakeGap  time.Duration
	onWake   func(Event)
	now      func() time.Time
	ticks    <-chan time.Time
	mu       sync.Mutex
	last     time.Time
}

func New(config Config) (*Watcher, error) {
	if config.Interval == 0 {
		config.Interval = DefaultInterval
	}
	if config.WakeGap == 0 {
		config.WakeGap = DefaultWakeGap
	}
	if config.Interval < 100*time.Millisecond || config.Interval > time.Minute ||
		config.WakeGap < 2*config.Interval || config.WakeGap > 30*time.Minute {
		return nil, errors.New("power wake watcher timing is invalid")
	}
	if config.OnWake == nil {
		return nil, errors.New("power wake callback is required")
	}
	if config.now == nil {
		config.now = time.Now
	}
	watcher := &Watcher{
		interval: config.Interval, wakeGap: config.WakeGap, onWake: config.OnWake,
		now: config.now, ticks: config.ticks,
	}
	watcher.last = wallTime(watcher.now())
	return watcher, nil
}

func (watcher *Watcher) Run(ctx context.Context) {
	if watcher == nil || ctx == nil {
		return
	}
	ticks := watcher.ticks
	var ticker *time.Ticker
	if ticks == nil {
		ticker = time.NewTicker(watcher.interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			watcher.Observe(watcher.now())
		}
	}
}

// Observe records a scheduler observation and reports whether it crossed the
// wake threshold. Run calls it for normal ticks; native platform hooks and E2E
// tests may also call it when they have a stronger suspend/resume signal.
func (watcher *Watcher) Observe(observedAt time.Time) bool {
	if watcher == nil {
		return false
	}
	now := wallTime(observedAt)
	watcher.mu.Lock()
	gap := now.Sub(watcher.last)
	watcher.last = now
	watcher.mu.Unlock()
	if gap < watcher.wakeGap {
		return false
	}
	watcher.onWake(Event{SleptFor: gap, DetectedAt: now})
	return true
}

// wallTime strips Go's monotonic component. On platforms whose monotonic
// source pauses during suspend, comparing monotonic values would hide sleep.
func wallTime(value time.Time) time.Time {
	return time.Unix(0, value.UnixNano()).In(value.Location())
}
