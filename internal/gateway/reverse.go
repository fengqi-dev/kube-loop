package gateway

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

const (
	pendingAcceptTimeout = 15 * time.Second
	udpAssociationIdle   = 60 * time.Second
)

type controlSession struct {
	conn   net.Conn
	server *Server
	mu     sync.Mutex
}

type interceptListener struct {
	id      string
	network byte
	port    uint16
	tcp     net.Listener
	udp     net.PacketConn
	cancel  chan struct{}
	server  *Server
	control *controlSession
}

type pendingStream struct {
	id        uint64
	network   byte
	ready     chan net.Conn
	tcpConn   net.Conn
	udpPacket net.PacketConn
	assoc     *udpAssociation
	timer     *time.Timer
}

type udpAssociation struct {
	remote    net.Addr
	first     []byte
	tunnelMu  sync.Mutex
	tunnel    net.Conn
	lastSeen  time.Time
	pendingID uint64
}

func (s *Server) handleControl(client net.Conn) {
	session := &controlSession{conn: client, server: s}
	s.mu.Lock()
	s.controls[session] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.removeControl(session)
		_ = client.Close()
	}()
	if err := tunnel.WriteStatus(client, nil); err != nil {
		return
	}
	for {
		message, err := tunnel.ReadControlMessage(client)
		if err != nil {
			return
		}
		switch message.Type {
		case tunnel.CtrlRegister:
			if err := s.registerIntercept(session, message); err != nil {
				_ = session.reply(tunnel.ControlMessage{Type: tunnel.CtrlError, Error: err.Error()})
				continue
			}
			_ = session.reply(tunnel.ControlMessage{Type: tunnel.CtrlAck})
		case tunnel.CtrlUnregister:
			s.unregisterIntercept(message.InterceptID)
			_ = session.reply(tunnel.ControlMessage{Type: tunnel.CtrlAck})
		default:
			_ = session.reply(tunnel.ControlMessage{
				Type: tunnel.CtrlError, Error: fmt.Sprintf("unsupported control type %d", message.Type),
			})
		}
	}
}

func (c *controlSession) reply(message tunnel.ControlMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return tunnel.WriteControlMessage(c.conn, message)
}

func (s *Server) removeControl(session *controlSession) {
	s.mu.Lock()
	delete(s.controls, session)
	var toClose []*interceptListener
	for id, listener := range s.listeners {
		if listener.control == session {
			toClose = append(toClose, listener)
			delete(s.listeners, id)
		}
	}
	s.mu.Unlock()
	for _, listener := range toClose {
		listener.stop()
	}
}

func (s *Server) registerIntercept(session *controlSession, message tunnel.ControlMessage) error {
	if message.ListenPort < 1024 {
		return errors.New("listen port must be >= 1024")
	}
	s.mu.Lock()
	if _, exists := s.listeners[message.InterceptID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("intercept %q already registered", message.InterceptID)
	}
	for _, listener := range s.listeners {
		if listener.port == message.ListenPort && listener.network == message.Network {
			s.mu.Unlock()
			return fmt.Errorf("listen port %d already in use", message.ListenPort)
		}
	}
	s.mu.Unlock()

	listener := &interceptListener{
		id:      message.InterceptID,
		network: message.Network,
		port:    message.ListenPort,
		cancel:  make(chan struct{}),
		server:  s,
		control: session,
	}
	address := fmt.Sprintf(":%d", message.ListenPort)
	switch message.Network {
	case tunnel.NetworkTCP:
		tcp, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("listen tcp: %w", err)
		}
		listener.tcp = tcp
		go listener.acceptTCP()
	case tunnel.NetworkUDP:
		udp, err := net.ListenPacket("udp4", "0.0.0.0:"+fmt.Sprintf("%d", message.ListenPort))
		if err != nil {
			return fmt.Errorf("listen udp: %w", err)
		}
		listener.udp = udp
		go listener.acceptUDP()
	default:
		return fmt.Errorf("unsupported network %d", message.Network)
	}

	s.mu.Lock()
	s.listeners[message.InterceptID] = listener
	s.mu.Unlock()
	s.logf("registered intercept %s on %s/%d", message.InterceptID, networkName(message.Network), message.ListenPort)
	return nil
}

func (s *Server) unregisterIntercept(id string) {
	s.mu.Lock()
	listener := s.listeners[id]
	delete(s.listeners, id)
	s.mu.Unlock()
	if listener != nil {
		listener.stop()
		s.logf("unregistered intercept %s", id)
	}
}

func (l *interceptListener) stop() {
	select {
	case <-l.cancel:
	default:
		close(l.cancel)
	}
	if l.tcp != nil {
		_ = l.tcp.Close()
	}
	if l.udp != nil {
		_ = l.udp.Close()
	}
}
