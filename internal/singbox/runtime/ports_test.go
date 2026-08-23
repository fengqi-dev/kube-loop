package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestProbeLocalDNSHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := probeLocalDNS(
		ctx,
		"127.0.0.1",
		1,
		"kubernetes.default.svc.cluster.local",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("probeLocalDNS error = %v, want context.Canceled", err)
	}
}
