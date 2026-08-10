//go:build e2e

package platform

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
)

func requirePlatformE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("KUBELOOP_PLATFORM_E2E") != "1" {
		t.Skip("set KUBELOOP_PLATFORM_E2E=1 after installing the real helper")
	}
}

func TestHelperServiceReady(t *testing.T) {
	requirePlatformE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status := helper.GetStatus(ctx)
	if !status.Installed || !status.Running || !status.CoreReady {
		t.Fatalf("helper is not ready: %+v", status)
	}
	if status.Protocol != helperprotocol.Version {
		t.Fatalf("protocol=%d want %d", status.Protocol, helperprotocol.Version)
	}

	client, err := helper.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ActiveSessions) != 0 {
		t.Fatalf("expected idle helper, active sessions=%v", response.ActiveSessions)
	}
}

func TestHelperRejectsInvalidToken(t *testing.T) {
	requirePlatformE2E(t)
	client, err := helper.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	client.Token = strings.Repeat("0", 64)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Ping(ctx); err == nil {
		t.Fatal("helper accepted an invalid IPC token")
	}
}

func TestHelperStopAllIsIdempotent(t *testing.T) {
	requirePlatformE2E(t)
	client, err := helper.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for attempt := 0; attempt < 2; attempt++ {
		response, err := client.StopAll(ctx)
		if err != nil {
			t.Fatalf("stop-all attempt %d: %v", attempt+1, err)
		}
		if len(response.ActiveSessions) != 0 {
			t.Fatalf("stop-all attempt %d left sessions: %v", attempt+1, response.ActiveSessions)
		}
	}
}
