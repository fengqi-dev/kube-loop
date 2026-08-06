//go:build e2e

package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func TestWaitClusterProbeStopsWithParentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	_, err := waitClusterProbe(
		ctx, fake.NewSimpleClientset(), "10.96.0.1", 53, "udp", "ping", "pong", time.Minute,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitClusterProbe() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waitClusterProbe() took %v after cancellation", elapsed)
	}
}
