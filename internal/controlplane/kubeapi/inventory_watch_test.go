package kubeapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
)

func TestInventoryWatchSharesInformerAndSlowSubscriberKeepsLatestSnapshot(
	t *testing.T,
) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			Name:      "pod-0",
			Namespace: "development",
		},
	)
	hub := newInventoryWatchHub(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subject := authorization.Subject{
		ID:     "identity-1",
		Groups: []string{"developers"},
	}
	slow, stopSlow, err := hub.subscribe(
		ctx,
		subject,
		client,
		"development",
		inventoryPods,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stopSlow()
	select {
	case <-slow:
	case <-ctx.Done():
		t.Fatal("initial slow-subscriber snapshot timed out")
	}
	// Fill the slow subscriber's one-slot mailbox and intentionally stop reading.
	if _, err := client.CoreV1().Pods("development").Create(ctx, &corev1.Pod{
		Name: "pod-1", Namespace: "development",
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	fast, stopFast, err := hub.subscribe(
		ctx,
		subject,
		client,
		"development",
		inventoryPods,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stopFast()
	hub.mu.Lock()
	if len(hub.feeds) != 1 {
		t.Fatalf("shared feeds = %d, want 1", len(hub.feeds))
	}
	hub.mu.Unlock()
	for index := 2; index < 32; index++ {
		if _, err := client.CoreV1().Pods("development").Create(ctx, &corev1.Pod{
			Name: fmt.Sprintf("pod-%d", index), Namespace: "development",
		}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for {
		select {
		case snapshot := <-fast:
			if len(snapshot.Pods) == 32 {
				if snapshot.SchemaVersion != 1 || snapshot.Type != "snapshot" ||
					snapshot.Namespace != "development" {
					t.Fatalf("snapshot binding = %#v", snapshot)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("fast subscriber was blocked behind slow subscriber")
		}
	}
}

func TestInventoryWatchLastUnsubscribeStopsFeed(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Service{
		Name: "api", Namespace: "development",
	})
	hub := newInventoryWatchHub(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	subject := authorization.Subject{ID: "identity-1"}

	updates, unsubscribe, err := hub.subscribe(
		ctx,
		subject,
		client,
		"development",
		inventoryServices,
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-updates:
	case <-ctx.Done():
		t.Fatal("initial Service snapshot timed out")
	}
	hub.mu.Lock()
	var feed *inventoryFeed
	for _, candidate := range hub.feeds {
		feed = candidate
	}
	hub.mu.Unlock()
	if feed == nil {
		t.Fatal("subscribed feed is missing")
	}

	unsubscribe()
	unsubscribe()
	hub.mu.Lock()
	remaining := len(hub.feeds)
	hub.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("feeds after last unsubscribe = %d", remaining)
	}
	select {
	case <-feed.stop:
	default:
		t.Fatal("last unsubscribe did not stop the feed")
	}
}
