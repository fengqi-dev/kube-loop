package httpapi

import (
	"sync"
	"time"
)

const maximumSourceBuckets = 4096

type limitBucket struct {
	started time.Time
	count   int
}

type exchangeLimiter struct {
	mu            sync.Mutex
	global        limitBucket
	sources       map[string]limitBucket
	globalMaximum int
	sourceMaximum int
	window        time.Duration
	now           func() time.Time
}

func newExchangeLimiter(globalMaximum, sourceMaximum int, window time.Duration) *exchangeLimiter {
	return &exchangeLimiter{
		sources: make(map[string]limitBucket), globalMaximum: globalMaximum,
		sourceMaximum: sourceMaximum, window: window, now: time.Now,
	}
}

func (limiter *exchangeLimiter) allow(source string) bool {
	now := limiter.now().UTC()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	global, globalAllowed := increment(limiter.global, now, limiter.window, limiter.globalMaximum)
	limiter.global = global
	if !globalAllowed {
		return false
	}
	current, exists := limiter.sources[source]
	if !exists && len(limiter.sources) >= maximumSourceBuckets {
		limiter.prune(now)
		if len(limiter.sources) >= maximumSourceBuckets {
			return false
		}
	}
	current, sourceAllowed := increment(current, now, limiter.window, limiter.sourceMaximum)
	limiter.sources[source] = current
	return sourceAllowed
}

func (limiter *exchangeLimiter) prune(now time.Time) {
	for key, bucket := range limiter.sources {
		if bucket.started.IsZero() || now.Sub(bucket.started) >= limiter.window {
			delete(limiter.sources, key)
		}
	}
}

func increment(bucket limitBucket, now time.Time, window time.Duration, maximum int) (limitBucket, bool) {
	if bucket.started.IsZero() || now.Sub(bucket.started) >= window {
		bucket = limitBucket{started: now}
	}
	if bucket.count >= maximum {
		return bucket, false
	}
	bucket.count++
	return bucket, true
}
