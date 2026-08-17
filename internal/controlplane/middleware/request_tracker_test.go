package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestTrackerDrainsActiveRequestAndRejectsNewRequest(t *testing.T) {
	tracker := NewRequestTracker()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := tracker.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writer.WriteHeader(http.StatusNoContent)
	}))

	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(firstResponse, httptest.NewRequest(http.MethodGet, "/", nil))
		close(firstDone)
	}()
	<-started

	tracker.BeginDrain()
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if secondResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("new request status = %d, want %d", secondResponse.Code, http.StatusServiceUnavailable)
	}

	waitContext, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := tracker.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}

	close(release)
	<-firstDone
	if firstResponse.Code != http.StatusNoContent {
		t.Fatalf("active request status = %d, want %d", firstResponse.Code, http.StatusNoContent)
	}
	if err := tracker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRequestTrackerWithoutActiveRequestsDrainsImmediately(t *testing.T) {
	tracker := NewRequestTracker()
	tracker.BeginDrain()
	if err := tracker.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
