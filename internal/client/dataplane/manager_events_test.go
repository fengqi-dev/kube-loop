package dataplane

import (
	"context"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func TestRecoveryFailureActionDistinguishesOperatorActions(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		reason    string
		retryable bool
	}{
		{name: "authentication", err: &remote.APIError{Status: 401}, reason: reasonAuthenticationRequired},
		{name: "access", err: &remote.APIError{Status: 403}, reason: reasonAccessDenied},
		{name: "session", err: &remote.APIError{Status: 404}, reason: reasonSessionExpired, retryable: true},
		{name: "network", err: context.DeadlineExceeded, reason: reasonNetworkUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason, retryable := recoveryFailureAction(test.err)
			if reason != test.reason || retryable != test.retryable {
				t.Fatalf("action = %q/%t, want %q/%t", reason, retryable, test.reason, test.retryable)
			}
		})
	}
}

func TestManagerStatusCallbackCannotBlockLifecycleEvents(t *testing.T) {
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})
	manager, err := NewManager(&testTickets{}, Config{OnStatus: func(StatusEvent) {
		select {
		case <-callbackEntered:
		default:
			close(callbackEntered)
		}
		<-releaseCallback
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(releaseCallback)
		_ = manager.Shutdown()
	}()
	manager.emit("service", Status{State: dataplaneConnected}, nil)
	select {
	case <-callbackEntered:
	case <-time.After(time.Second):
		t.Fatal("status callback was not invoked")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for generation := uint64(1); generation <= 100; generation++ {
			manager.emit("service", Status{State: dataplaneConnected, SessionGeneration: generation}, nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked status callback stalled lifecycle event publication")
	}
}
