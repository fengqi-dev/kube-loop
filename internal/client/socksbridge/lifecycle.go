package socksbridge

import (
	"errors"
	"net"
	"sync"
)

type goroutinePool struct {
	mu     sync.Mutex
	active int
	idle   *sync.Cond
}

func newGoroutinePool() *goroutinePool {
	pool := &goroutinePool{}
	pool.idle = sync.NewCond(&pool.mu)
	return pool
}

func (pool *goroutinePool) Submit(task func()) error {
	pool.mu.Lock()
	pool.active++
	pool.mu.Unlock()
	go func() {
		defer func() {
			pool.mu.Lock()
			pool.active--
			if pool.active == 0 {
				pool.idle.Broadcast()
			}
			pool.mu.Unlock()
		}()
		task()
	}()
	return nil
}

func (pool *goroutinePool) Wait() {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for pool.active > 0 {
		pool.idle.Wait()
	}
}

type trackedListener struct {
	net.Listener

	mu          sync.Mutex
	closed      bool
	connections map[*trackedClientConn]struct{}
}

func newTrackedListener(listener net.Listener) *trackedListener {
	return &trackedListener{
		Listener:    listener,
		connections: make(map[*trackedClientConn]struct{}),
	}
}

func (listener *trackedListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tracked := &trackedClientConn{Conn: connection}
	tracked.onClose = func() {
		listener.mu.Lock()
		delete(listener.connections, tracked)
		listener.mu.Unlock()
	}
	listener.mu.Lock()
	if listener.closed {
		listener.mu.Unlock()
		_ = tracked.Close()
		return nil, net.ErrClosed
	}
	listener.connections[tracked] = struct{}{}
	listener.mu.Unlock()
	return tracked, nil
}

func (listener *trackedListener) Close() error {
	listener.mu.Lock()
	listener.closed = true
	connections := make([]*trackedClientConn, 0, len(listener.connections))
	for connection := range listener.connections {
		connections = append(connections, connection)
	}
	listener.mu.Unlock()
	result := listener.Listener.Close()
	for _, connection := range connections {
		result = errors.Join(result, connection.Close())
	}
	return result
}

type trackedClientConn struct {
	net.Conn

	onClose func()
	once    sync.Once
	err     error
}

func (connection *trackedClientConn) Close() error {
	connection.once.Do(func() {
		connection.err = connection.Conn.Close()
		connection.onClose()
	})
	return connection.err
}
