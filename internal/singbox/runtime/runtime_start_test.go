package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type blockingRoundTripper struct{}

func (blockingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestStartWithPortCollisionRetry(t *testing.T) {
	attempts := 0
	core, err := startWithPortCollisionRetry(
		context.Background(),
		func() (singbox.RunningCore, error) {
			attempts++
			if attempts < startPortCollisionAttempts {
				return nil, errors.New(
					"external controller listen error: " +
						"listen tcp 127.0.0.1:37199: bind: address already in use",
				)
			}
			//nolint:nilnil // A nil core models a successful no-op start for this retry unit test.
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("retry port collision: %v", err)
	}
	if core != nil {
		t.Fatal("expected nil test core")
	}
	if attempts != startPortCollisionAttempts {
		t.Fatalf("attempts=%d want %d", attempts, startPortCollisionAttempts)
	}
}

func TestStartWithPortCollisionRetryStopsAfterLimit(t *testing.T) {
	attempts := 0
	_, err := startWithPortCollisionRetry(
		context.Background(),
		func() (singbox.RunningCore, error) {
			attempts++
			return nil, errors.New("listen tcp: address already in use")
		},
	)
	if err == nil {
		t.Fatal("expected retry limit error")
	}
	if attempts != startPortCollisionAttempts {
		t.Fatalf("attempts=%d want %d", attempts, startPortCollisionAttempts)
	}
	if !strings.Contains(err.Error(), "after 3 port allocation attempts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartWithPortCollisionRetryReturnsOtherErrors(t *testing.T) {
	attempts := 0
	wantErr := errors.New("helper authentication failed")
	_, err := startWithPortCollisionRetry(
		context.Background(),
		func() (singbox.RunningCore, error) {
			attempts++
			return nil, wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d want 1", attempts)
	}
}

func TestIsAddressAlreadyInUseRecognizesWindowsError(t *testing.T) {
	err := errors.New(
		"bind: Only one usage of each socket address " +
			"(protocol/network address/port) is normally permitted.",
	)
	if !isAddressAlreadyInUse(err) {
		t.Fatalf("expected Windows port collision to be recognized: %v", err)
	}
}

func TestWaitReadyBoundsBlockingControllerRequest(t *testing.T) {
	runtime := &Runtime{
		HTTPClient:    &http.Client{Transport: blockingRoundTripper{}},
		readyTimeout:  50 * time.Millisecond,
		readyInterval: 10 * time.Millisecond,
	}
	process := &Process{
		done:              make(chan struct{}),
		controllerAddress: "127.0.0.1:1",
		httpClient:        runtime.HTTPClient,
	}
	started := time.Now()
	err := runtime.waitReady(context.Background(), process)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitReady error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("waitReady exceeded bounded timeout: %s", elapsed)
	}
}
