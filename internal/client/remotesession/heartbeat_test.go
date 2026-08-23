package remotesession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type blockingHeartbeatGateway struct {
	*fakeGateway

	started chan string
	release chan struct{}
}

func (gateway *blockingHeartbeatGateway) HeartbeatSession(
	ctx context.Context,
	serverProfile profile.Profile,
	current remote.Session,
) (remote.Session, error) {
	gateway.started <- serverProfile.ID
	select {
	case <-gateway.release:
		return gateway.fakeGateway.HeartbeatSession(ctx, serverProfile, current)
	case <-ctx.Done():
		return current, ctx.Err()
	}
}

func TestHeartbeatFailureBlocksTicketsUntilRefreshRecovers(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"}
	if _, err := manager.Connect(context.Background(), serverProfile, "development"); err != nil {
		t.Fatal(err)
	}
	heartbeatFailure := errors.New("heartbeat unavailable")
	gateway.mu.Lock()
	gateway.heartbeatErr = heartbeatFailure
	gateway.mu.Unlock()
	if _, err := manager.Refresh(context.Background(), serverProfile.ID); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("refresh error = %v", err)
	}
	if _, err := manager.Current(serverProfile.ID); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("current error = %v", err)
	}
	if _, err := manager.IssueRelayTicket(context.Background(), serverProfile.ID); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("ticket error = %v", err)
	}
	gateway.mu.Lock()
	if gateway.tickets != 0 {
		gateway.mu.Unlock()
		t.Fatalf("tickets issued during heartbeat failure = %d", gateway.tickets)
	}
	gateway.heartbeatErr = nil
	gateway.mu.Unlock()
	if _, err := manager.Refresh(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.IssueRelayTicket(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}

func TestHeartbeatRefreshesProfilesConcurrently(t *testing.T) {
	gateway := &blockingHeartbeatGateway{
		fakeGateway: &fakeGateway{},
		started:     make(chan string, 2),
		release:     make(chan struct{}),
	}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gateway.release) }) }
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	for _, profileID := range []string{"service-1", "service-2"} {
		if _, err := manager.Connect(
			context.Background(),
			profile.Profile{ID: profileID, BaseURL: "https://gateway.example.test"},
			"development",
		); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() {
		manager.heartbeat()
		close(done)
	}()
	started := map[string]bool{}
	for len(started) < 2 {
		select {
		case profileID := <-gateway.started:
			started[profileID] = true
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("concurrent heartbeat starts = %#v, want both profiles", started)
		}
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent heartbeat did not finish")
	}
}
