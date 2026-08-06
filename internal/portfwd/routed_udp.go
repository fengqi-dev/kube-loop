package portfwd

import (
	"context"
	"maps"
	"net"
	"slices"
	"sync"
	"time"
)

type routedUDPForwarder struct {
	socket       *net.UDPConn
	target       string
	dialer       TrafficDialer
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	associations map[string]*udpAssociation
	wg           sync.WaitGroup
	once         sync.Once
}

type udpAssociation struct {
	upstream net.Conn
	client   *net.UDPAddr
}

var _ Forwarder = (*routedUDPForwarder)(nil)

func newRoutedUDPForwarder(
	socket *net.UDPConn, target string, dialer TrafficDialer,
) *routedUDPForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	forwarder := &routedUDPForwarder{
		socket: socket, target: target, dialer: dialer, ctx: ctx, cancel: cancel,
		associations: make(map[string]*udpAssociation),
	}
	forwarder.wg.Go(forwarder.serve)
	return forwarder
}

func (f *routedUDPForwarder) Address() string { return f.socket.LocalAddr().String() }

func (f *routedUDPForwarder) Close() error {
	var err error
	f.once.Do(func() {
		f.cancel()
		err = f.socket.Close()
		f.mu.Lock()
		items := slices.Collect(maps.Values(f.associations))
		clear(f.associations)
		f.mu.Unlock()
		for _, item := range items {
			_ = item.upstream.Close()
		}
		f.wg.Wait()
	})
	return err
}

func (f *routedUDPForwarder) serve() {
	buffer := make([]byte, 65535)
	for {
		n, client, err := f.socket.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		item, err := f.association(client)
		if err != nil {
			continue
		}
		if _, err := item.upstream.Write(buffer[:n]); err != nil {
			f.removeAssociation(client.String(), item)
		}
	}
}

func (f *routedUDPForwarder) association(client *net.UDPAddr) (*udpAssociation, error) {
	key := client.String()
	f.mu.Lock()
	if item := f.associations[key]; item != nil {
		f.mu.Unlock()
		return item, nil
	}
	f.mu.Unlock()

	upstream, err := f.dialer.DialContext(f.ctx, "udp", f.target)
	if err != nil {
		return nil, err
	}
	item := &udpAssociation{upstream: upstream, client: client}
	f.mu.Lock()
	if f.ctx.Err() != nil {
		f.mu.Unlock()
		_ = upstream.Close()
		return nil, context.Canceled
	}
	if existing := f.associations[key]; existing != nil {
		f.mu.Unlock()
		_ = upstream.Close()
		return existing, nil
	}
	f.associations[key] = item
	f.mu.Unlock()
	f.wg.Go(func() { f.readReplies(key, item) })
	return item, nil
}

func (f *routedUDPForwarder) readReplies(key string, item *udpAssociation) {
	defer f.removeAssociation(key, item)
	buffer := make([]byte, 65535)
	for {
		_ = item.upstream.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := item.upstream.Read(buffer)
		if err != nil {
			return
		}
		if _, err := f.socket.WriteToUDP(buffer[:n], item.client); err != nil {
			return
		}
	}
}

func (f *routedUDPForwarder) removeAssociation(key string, item *udpAssociation) {
	f.mu.Lock()
	if f.associations[key] == item {
		delete(f.associations, key)
	}
	f.mu.Unlock()
	_ = item.upstream.Close()
}
