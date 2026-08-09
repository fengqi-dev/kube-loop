package websocketmux

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/xtaci/smux"
)

type ClientConfig struct {
	URL               string
	Token             string
	TLSConfig         *tls.Config
	PoolSize          int
	MaxPhysical       int
	MaxStreamsPerConn int
}

type pooledSession struct {
	ws      *websocket.Conn
	session *smux.Session
}

// Forwarder exposes a loopback TCP endpoint and maps each accepted connection
// to an independent smux stream over a small pool of WebSocket connections.
type Forwarder struct {
	ctx      context.Context
	cancel   context.CancelFunc
	listener net.Listener
	config   ClientConfig

	mu        sync.Mutex
	sessions  []*pooledSession
	dialMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func Start(ctx context.Context, config ClientConfig) (*Forwarder, error) {
	parsed, err := url.ParseRequestURI(config.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid Gateway WebSocket URL: %w", err)
	}
	if (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
		return nil, errors.New("Gateway WebSocket URL must use ws:// or wss://")
	}
	if config.Token == "" {
		return nil, errors.New("Gateway WebSocket token is required")
	}
	if config.PoolSize <= 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.MaxPhysical <= 0 {
		config.MaxPhysical = defaultMaxPhysical
	}
	if config.MaxPhysical < config.PoolSize {
		config.MaxPhysical = config.PoolSize
	}
	if config.MaxStreamsPerConn <= 0 {
		config.MaxStreamsPerConn = defaultMaxStreams
	}
	if config.PoolSize > maxPoolSize || config.MaxPhysical > maxPhysicalConnections ||
		config.MaxStreamsPerConn > maxStreamsPerConnection {
		return nil, errors.New("Gateway multiplexing limits are too large")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local Gateway streams: %w", err)
	}
	forwardCtx, cancel := context.WithCancel(ctx)
	forwarder := &Forwarder{ctx: forwardCtx, cancel: cancel, listener: listener, config: config}
	for range config.PoolSize {
		session, dialErr := forwarder.dial()
		if dialErr != nil {
			if len(forwarder.sessions) == 0 {
				_ = listener.Close()
				cancel()
				return nil, dialErr
			}
			break
		}
		forwarder.sessions = append(forwarder.sessions, session)
	}
	forwarder.wg.Add(1)
	go forwarder.acceptLoop()
	return forwarder, nil
}

func (f *Forwarder) Address() string { return f.listener.Addr().String() }

func (f *Forwarder) Close() error {
	var closeErr error
	f.closeOnce.Do(func() {
		f.cancel()
		closeErr = f.listener.Close()
		f.mu.Lock()
		sessions := append([]*pooledSession(nil), f.sessions...)
		f.sessions = nil
		f.mu.Unlock()
		for _, item := range sessions {
			_ = item.session.Close()
			_ = item.ws.Close(websocket.StatusNormalClosure, "client shutdown")
		}
		f.wg.Wait()
	})
	return closeErr
}

func (f *Forwarder) acceptLoop() {
	defer f.wg.Done()
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.forward(connection)
		}()
	}
}

func (f *Forwarder) forward(local net.Conn) {
	stream, err := f.openStream()
	if err != nil {
		_ = local.Close()
		return
	}
	defer stream.Close()
	defer local.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(stream, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, stream); done <- struct{}{} }()
	<-done
}

func (f *Forwarder) openStream() (*smux.Stream, error) {
	for attempt := 0; attempt < 2; attempt++ {
		item := f.pickSession()
		if item != nil {
			stream, err := item.session.OpenStream()
			if err == nil {
				return stream, nil
			}
			f.discard(item)
		}
		if _, err := f.ensureSession(); err != nil && attempt == 1 {
			return nil, err
		}
	}
	return nil, errors.New("no healthy Gateway WebSocket session")
}

func (f *Forwarder) pickSession() *pooledSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	var selected *pooledSession
	for _, item := range f.sessions {
		if item.session.IsClosed() || item.session.NumStreams() >= f.config.MaxStreamsPerConn {
			continue
		}
		if selected == nil || item.session.NumStreams() < selected.session.NumStreams() {
			selected = item
		}
	}
	return selected
}

func (f *Forwarder) ensureSession() (*pooledSession, error) {
	f.dialMu.Lock()
	defer f.dialMu.Unlock()
	if item := f.pickSession(); item != nil {
		return item, nil
	}
	f.mu.Lock()
	count := len(f.sessions)
	f.mu.Unlock()
	if count >= f.config.MaxPhysical {
		return nil, errors.New("all Gateway WebSocket sessions are at capacity")
	}
	item, err := f.dial()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.sessions = append(f.sessions, item)
	f.mu.Unlock()
	return item, nil
}

func (f *Forwarder) discard(target *pooledSession) {
	f.mu.Lock()
	for index, item := range f.sessions {
		if item == target {
			f.sessions = append(f.sessions[:index], f.sessions[index+1:]...)
			break
		}
	}
	f.mu.Unlock()
	_ = target.session.Close()
	_ = target.ws.Close(websocket.StatusGoingAway, "session replaced")
}

func (f *Forwarder) dial() (*pooledSession, error) {
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+f.config.Token)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if f.config.TLSConfig != nil {
		transport.TLSClientConfig = f.config.TLSConfig.Clone()
	}
	httpClient := &http.Client{Transport: transport}
	dialCtx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(dialCtx, f.config.URL, &websocket.DialOptions{
		HTTPClient:   httpClient,
		HTTPHeader:   header,
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("dial Gateway WebSocket: HTTP %s: %w", response.Status, err)
		}
		return nil, fmt.Errorf("dial Gateway WebSocket: %w", err)
	}
	if connection.Subprotocol() != Subprotocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "subprotocol required")
		return nil, errors.New("Gateway did not negotiate the multiplexing subprotocol")
	}
	streamConn := websocket.NetConn(f.ctx, connection, websocket.MessageBinary)
	connection.SetReadLimit(1024 * 1024)
	session, err := smux.Client(streamConn, smuxConfig())
	if err != nil {
		_ = connection.Close(websocket.StatusInternalError, "multiplexer setup failed")
		return nil, fmt.Errorf("start Gateway multiplexer: %w", err)
	}
	item := &pooledSession{ws: connection, session: session}
	f.wg.Add(1)
	go f.keepAlive(item)
	return item, nil
}

func (f *Forwarder) keepAlive(item *pooledSession) {
	defer f.wg.Done()
	ticker := time.NewTicker(defaultKeepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-item.session.CloseChan():
			f.discard(item)
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			err := item.ws.Ping(ctx)
			cancel()
			if err != nil {
				f.discard(item)
				return
			}
		}
	}
}

func smuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.Version = 2
	config.KeepAliveInterval = defaultKeepAliveInterval
	config.KeepAliveTimeout = defaultKeepAliveTimeout
	config.MaxReceiveBuffer = 4 * 1024 * 1024
	config.MaxStreamBuffer = 512 * 1024
	return config
}
