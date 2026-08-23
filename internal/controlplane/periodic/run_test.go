package periodic_test

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/periodic"
)

func TestRunSkipsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var calls atomic.Int64
	periodic.Run(ctx, time.Second, func(context.Context) {
		calls.Add(1)
	})
	if got := calls.Load(); got != 0 {
		t.Fatalf("operation calls = %d, want 0", got)
	}
}

func TestRunWaitsAfterOperationCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Second
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var calls atomic.Int64
		done := make(chan struct{})
		go func() {
			periodic.Run(ctx, interval, func(operationContext context.Context) {
				if calls.Add(1) == 1 {
					select {
					case <-time.After(2 * interval):
					case <-operationContext.Done():
					}
				}
			})
			close(done)
		}()

		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("calls after slow first pass = %d, want 1", got)
		}
		time.Sleep(2 * interval)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("calls when first pass completes = %d, want 1", got)
		}
		time.Sleep(interval - time.Nanosecond)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("calls before next interval = %d, want 1", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("calls at next interval = %d, want 2", got)
		}

		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("Run did not stop after cancellation")
		}
	})
}

func TestRunAfterWaitsBeforeFirstOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = time.Second
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var calls atomic.Int64
		done := make(chan struct{})
		go func() {
			periodic.RunAfter(ctx, interval, func(context.Context) {
				calls.Add(1)
			})
			close(done)
		}()

		synctest.Wait()
		if got := calls.Load(); got != 0 {
			t.Fatalf("initial operation calls = %d, want 0", got)
		}
		time.Sleep(interval - time.Nanosecond)
		synctest.Wait()
		if got := calls.Load(); got != 0 {
			t.Fatalf("calls before first interval = %d, want 0", got)
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Fatalf("calls at first interval = %d, want 1", got)
		}

		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("RunAfter did not stop after cancellation")
		}
	})
}
