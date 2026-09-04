package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

// Server is the privileged helper RPC server.
type Server struct {
	Auth AuthFile
	Log  *log.Logger

	mu        sync.Mutex
	sessions  map[string]*session
	lifecycle sync.Mutex
	closing   atomic.Bool

	connectionMu sync.Mutex
	connections  map[net.Conn]struct{}
	handlers     sync.WaitGroup
}

type session struct {
	lifecycleMu sync.Mutex
	stopping    bool

	workDir    string
	cmd        *exec.Cmd
	done       chan struct{}
	exited     chan sessionExit
	routes     []string
	dns        sessionspec.DNSMeta
	tunAddress string
}

type sessionExit struct {
	err error
	log string
}

func NewServer(auth AuthFile) *Server {
	return &Server{
		Auth:        auth,
		Log:         log.Default(),
		sessions:    map[string]*session{},
		connections: map[net.Conn]struct{}{},
	}
}

func (s *Server) Serve(ctx context.Context) error {
	listener, err := listenHelper(s.Auth.OwnerSID)
	if err != nil {
		return err
	}
	return s.serve(ctx, listener)
}

func (s *Server) serve(ctx context.Context, listener net.Listener) error {
	defer func() {
		s.closing.Store(true)
		_ = listener.Close()
		s.closeConnections()
		s.handlers.Wait()
		s.stopAllSessions()
	}()
	stopListener := context.AfterFunc(ctx, func() {
		s.closing.Store(true)
		_ = listener.Close()
		s.closeConnections()
	})
	defer stopListener()

	s.Log.Printf("kubeloop-helper listening on %s (version %s)", SocketPath(), Version)
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		if !s.trackConnection(conn) {
			_ = conn.Close()
			continue
		}
		go func() {
			defer s.finishConnection(conn)
			s.handle(conn)
		}()
	}
}

func (s *Server) trackConnection(conn net.Conn) bool {
	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if s.closing.Load() {
		return false
	}
	s.connections[conn] = struct{}{}
	s.handlers.Add(1)
	return true
}

func (s *Server) finishConnection(conn net.Conn) {
	s.connectionMu.Lock()
	delete(s.connections, conn)
	s.connectionMu.Unlock()
	s.handlers.Done()
}

func (s *Server) closeConnections() {
	s.connectionMu.Lock()
	connections := make([]net.Conn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.connectionMu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func (s *Server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	decoder := json.NewDecoder(bufio.NewReader(conn))
	decoder.DisallowUnknownFields()
	var request helperrpc.Request
	if err := decoder.Decode(&request); err != nil {
		_ = writeResponse(conn, helperrpc.Response{OK: false, Error: "invalid request"})
		return
	}
	response := s.dispatch(request)
	if err := writeResponse(conn, response); err != nil &&
		request.Op == helperrpc.OpStart && response.OK && request.Session != nil {
		_ = s.stopSession(request.Session.ID)
	}
}

func (s *Server) dispatch(request helperrpc.Request) helperrpc.Response {
	if request.Token == "" || request.Token != s.Auth.Token {
		return helperrpc.Response{OK: false, Error: "unauthorized"}
	}
	switch request.Op {
	case helperrpc.OpPing, helperrpc.OpStatus:
		_, coreErr := resolveSingBoxPath(s.Auth)
		activeSessions, pid := s.activeSessionState()
		return helperrpc.Response{
			OK: true, Version: Version, Protocol: helperrpc.Version,
			Installed: true, Running: true, CoreReady: coreErr == nil,
			ActiveSessions: activeSessions, PID: pid,
		}
	case helperrpc.OpStart:
		if request.Session == nil {
			return helperrpc.Response{OK: false, Error: "session is required"}
		}
		s.Log.Printf("starting privileged session %s", request.Session.ID)
		if err := s.startSession(*request.Session); err != nil {
			s.Log.Printf("start privileged session %s: %v", request.Session.ID, err)
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("privileged session %s started", request.Session.ID)
		return helperrpc.Response{OK: true, Version: Version, Protocol: helperrpc.Version}
	case helperrpc.OpStop:
		if err := singbox.ValidateSessionID(request.SessionID); err != nil {
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("stopping privileged session %s", request.SessionID)
		if err := s.stopSession(request.SessionID); err != nil {
			s.Log.Printf("stop privileged session %s: %v", request.SessionID, err)
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("privileged session %s stopped", request.SessionID)
		return helperrpc.Response{OK: true, Version: Version, Protocol: helperrpc.Version}
	case helperrpc.OpStopAll:
		s.Log.Printf("stopping all privileged sessions")
		s.stopAllSessions()
		s.Log.Printf("all privileged sessions stopped")
		return helperrpc.Response{OK: true, Version: Version, Protocol: helperrpc.Version}
	case helperrpc.OpUpdateDNS:
		if err := singbox.ValidateSessionID(request.SessionID); err != nil {
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		if request.DNS == nil {
			return helperrpc.Response{OK: false, Error: "dns is required"}
		}
		s.Log.Printf(
			"updating split DNS for session %s: domains=%d",
			request.SessionID, len(request.DNS.Domains),
		)
		if err := s.updateSessionDNS(request.SessionID, *request.DNS); err != nil {
			s.Log.Printf("update split DNS for session %s: %v", request.SessionID, err)
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("split DNS updated for session %s", request.SessionID)
		return helperrpc.Response{OK: true, Version: Version, Protocol: helperrpc.Version}
	case helperrpc.OpReadLogs:
		if err := singbox.ValidateSessionID(request.SessionID); err != nil {
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		data, offset, err := s.readSessionLogs(request.SessionID, request.LogOffset)
		if err != nil {
			return helperrpc.Response{OK: false, Error: err.Error()}
		}
		return helperrpc.Response{
			OK: true, Version: Version, Protocol: helperrpc.Version,
			LogData: data, LogOffset: offset,
		}
	default:
		return helperrpc.Response{OK: false, Error: fmt.Sprintf("unsupported op %q", request.Op)}
	}
}

func (s *Server) activeSessionState() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.sessions))
	pid := 0
	for id, current := range s.sessions {
		ids = append(ids, id)
		if current.cmd != nil && current.cmd.Process != nil {
			pid = current.cmd.Process.Pid
		}
	}
	return ids, pid
}

func writeResponse(w io.Writer, response helperrpc.Response) error {
	return json.NewEncoder(w).Encode(response)
}
