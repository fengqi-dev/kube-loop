package traffic

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type stubDialer struct {
	conn net.Conn
}

func (d stubDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

func TestTrackedDialerRecordsFeatureAndBytes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()
	raw, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	tracker := NewTracker()
	dialer := TrackedDialer{
		Inner:   stubDialer{conn: raw},
		Feature: "exchange",
		Tracker: tracker,
	}
	conn, err := dialer.DialContext(context.Background(), "tcp", "127.0.0.1:8000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	snap := tracker.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot = %d", len(snap))
	}
	if snap[0].Feature != "exchange" || snap[0].Destination != "127.0.0.1:8000" {
		t.Fatalf("unexpected snapshot %#v", snap[0])
	}
	if snap[0].Upload != 5 {
		t.Fatalf("upload = %d", snap[0].Upload)
	}
	_, port, err := net.SplitHostPort(snap[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	if got := tracker.FeatureBySourcePort(port); got != "exchange" {
		t.Fatalf("FeatureBySourcePort = %q", got)
	}
	_ = conn.Close()
	closed := tracker.Snapshot()
	if len(closed) != 1 || !closed[0].Closed {
		t.Fatalf("expected retained closed connection, got %#v", closed)
	}
}

func TestTrackerPrunesOldClosedConnections(t *testing.T) {
	tracker := NewTracker()
	tracker.conns["old"] = &tracked{
		feature:  "preview",
		closedAt: time.Now().Add(-time.Minute),
	}
	if snap := tracker.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected prune, got %#v", snap)
	}
}
