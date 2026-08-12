package middleware

import (
	"context"
	"net/http"
	"sync"
)

// RequestTracker rejects new requests during shutdown and waits for requests
// that were already accepted to finish.
type RequestTracker struct {
	mu         sync.Mutex
	active     int
	isDraining bool
	done       chan struct{}
	isClosed   bool
}

func NewRequestTracker() *RequestTracker {
	return &RequestTracker{done: make(chan struct{})}
}

func (t *RequestTracker) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !t.begin() {
			writer.Header().Set("Connection", "close")
			http.Error(writer, "server is shutting down", http.StatusServiceUnavailable)
			return
		}
		defer t.end()
		next.ServeHTTP(writer, request)
	})
}

func (t *RequestTracker) BeginDrain() {
	t.mu.Lock()
	t.isDraining = true
	t.closeDoneLocked()
	t.mu.Unlock()
}

func (t *RequestTracker) Wait(ctx context.Context) error {
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *RequestTracker) begin() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDraining {
		return false
	}
	t.active++
	return true
}

func (t *RequestTracker) end() {
	t.mu.Lock()
	t.active--
	t.closeDoneLocked()
	t.mu.Unlock()
}

func (t *RequestTracker) closeDoneLocked() {
	if t.isDraining && t.active == 0 && !t.isClosed {
		t.isClosed = true
		close(t.done)
	}
}
