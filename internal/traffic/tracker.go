package traffic

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const trackerRetainFor = 30 * time.Second

// LiveConn is one Adapter-side connection open or recently closed through a
// feature-dyed SOCKS dialer.
type LiveConn struct {
	ID          string
	Feature     string
	Network     string
	Source      string
	Destination string
	StartedAt   time.Time
	Upload      int64
	Download    int64
	Closed      bool
}

// Tracker attributes feature traffic that sing-box clash_api omits (no metadata.user).
type Tracker struct {
	mu    sync.Mutex
	conns map[string]*tracked
}

type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type tracked struct {
	feature     string
	network     string
	source      string
	sourcePort  string
	destination string
	startedAt   time.Time
	closedAt    time.Time
	upload      atomic.Int64
	download    atomic.Int64
}

func NewTracker() *Tracker {
	return &Tracker{conns: make(map[string]*tracked)}
}

// TrackedDialer dials through an inner dialer and records feature + byte counts.
type TrackedDialer struct {
	Inner   ContextDialer
	Feature string
	Tracker *Tracker
}

func (d TrackedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.Tracker == nil || d.Feature == "" {
		return d.Inner.DialContext(ctx, network, address)
	}
	conn, err := d.Inner.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	source := ""
	sourcePort := ""
	if local := conn.LocalAddr(); local != nil {
		source = local.String()
		if _, port, splitErr := net.SplitHostPort(source); splitErr == nil {
			sourcePort = port
		}
	}
	entry := &tracked{
		feature:     d.Feature,
		network:     network,
		source:      source,
		sourcePort:  sourcePort,
		destination: address,
		startedAt:   time.Now(),
	}
	d.Tracker.mu.Lock()
	d.Tracker.conns[id] = entry
	d.Tracker.mu.Unlock()
	return &trackedConn{
		Conn:    conn,
		tracker: d.Tracker,
		id:      id,
		entry:   entry,
	}, nil
}

// Snapshot returns open and recently closed Adapter connections.
func (t *Tracker) Snapshot() []LiveConn {
	if t == nil {
		return nil
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]LiveConn, 0, len(t.conns))
	for id, item := range t.conns {
		if !item.closedAt.IsZero() && now.Sub(item.closedAt) > trackerRetainFor {
			delete(t.conns, id)
			continue
		}
		out = append(out, LiveConn{
			ID:          id,
			Feature:     item.feature,
			Network:     item.network,
			Source:      item.source,
			Destination: item.destination,
			StartedAt:   item.startedAt,
			Upload:      item.upload.Load(),
			Download:    item.download.Load(),
			Closed:      !item.closedAt.IsZero(),
		})
	}
	return out
}

// FeatureBySourcePort returns the feature dye for a clash connection sourced
// from the given loopback port (the Adapter→traffic-in TCP port).
func (t *Tracker) FeatureBySourcePort(port string) string {
	if t == nil || port == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, item := range t.conns {
		if item.sourcePort == port {
			return item.feature
		}
	}
	return ""
}

func (t *Tracker) close(id string) {
	t.mu.Lock()
	if item, ok := t.conns[id]; ok && item.closedAt.IsZero() {
		item.closedAt = time.Now()
	}
	t.mu.Unlock()
}

type trackedConn struct {
	net.Conn
	tracker *Tracker
	id      string
	entry   *tracked
	once    sync.Once
}

func (c *trackedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.entry.download.Add(int64(n))
	}
	if err == io.EOF {
		c.closeTracked()
	}
	return n, err
}

func (c *trackedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.entry.upload.Add(int64(n))
	}
	return n, err
}

func (c *trackedConn) Close() error {
	c.closeTracked()
	return c.Conn.Close()
}

func (c *trackedConn) closeTracked() {
	c.once.Do(func() {
		if c.tracker != nil {
			c.tracker.close(c.id)
		}
	})
}

var (
	_ ContextDialer = Dialer{}
	_ ContextDialer = TrackedDialer{}
)
