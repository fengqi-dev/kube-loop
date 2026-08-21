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

	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

// Server is the privileged helper RPC server.
type Server struct {
	Auth AuthFile
	Log  *log.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	lifecycleMu sync.Mutex
	stopping    bool

	workDir    string
	cmd        *exec.Cmd
	done       chan struct{}
	exited     chan sessionExit
	routes     []string
	dns        singbox.DNSMeta
	tunAddress string
}

type sessionExit struct {
	err error
	log string
}

func NewServer(auth AuthFile) *Server {
	return &Server{
		Auth:     auth,
		Log:      log.Default(),
		sessions: map[string]*session{},
	}
}

func (s *Server) Serve(ctx context.Context) error {
	listener, err := listenHelper(s.Auth.OwnerSID)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	s.Log.Printf("kubeloop-helper listening on %s (version %s)", SocketPath(), Version)
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				s.stopAllSessions()
				return nil
			default:
				return err
			}
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	decoder := json.NewDecoder(bufio.NewReader(conn))
	decoder.DisallowUnknownFields()
	var request helperprotocol.Request
	if err := decoder.Decode(&request); err != nil {
		_ = writeResponse(conn, helperprotocol.Response{OK: false, Error: "invalid request"})
		return
	}
	response := s.dispatch(request)
	if err := writeResponse(conn, response); err != nil &&
		request.Op == helperprotocol.OpStart && response.OK && request.Session != nil {
		_ = s.stopSession(request.Session.ID)
	}
}

func (s *Server) dispatch(request helperprotocol.Request) helperprotocol.Response {
	if request.Token == "" || request.Token != s.Auth.Token {
		return helperprotocol.Response{OK: false, Error: "unauthorized"}
	}
	switch request.Op {
	case helperprotocol.OpPing, helperprotocol.OpStatus:
		_, coreErr := resolveSingBoxPath(s.Auth)
		activeSessions, pid := s.activeSessionState()
		return helperprotocol.Response{
			OK: true, Version: Version, Protocol: helperprotocol.Version,
			Installed: true, Running: true, CoreReady: coreErr == nil,
			ActiveSessions: activeSessions, PID: pid,
		}
	case helperprotocol.OpStart:
		if request.Session == nil {
			return helperprotocol.Response{OK: false, Error: "session is required"}
		}
		s.Log.Printf("starting privileged session %s", request.Session.ID)
		if err := s.startSession(*request.Session); err != nil {
			s.Log.Printf("start privileged session %s: %v", request.Session.ID, err)
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("privileged session %s started", request.Session.ID)
		return helperprotocol.Response{OK: true, Version: Version, Protocol: helperprotocol.Version}
	case helperprotocol.OpStop:
		if err := singbox.ValidateSessionID(request.SessionID); err != nil {
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("stopping privileged session %s", request.SessionID)
		if err := s.stopSession(request.SessionID); err != nil {
			s.Log.Printf("stop privileged session %s: %v", request.SessionID, err)
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("privileged session %s stopped", request.SessionID)
		return helperprotocol.Response{OK: true, Version: Version, Protocol: helperprotocol.Version}
	case helperprotocol.OpStopAll:
		s.Log.Printf("stopping all privileged sessions")
		s.stopAllSessions()
		s.Log.Printf("all privileged sessions stopped")
		return helperprotocol.Response{OK: true, Version: Version, Protocol: helperprotocol.Version}
	case helperprotocol.OpUpdateDNS:
		if err := singbox.ValidateSessionID(request.SessionID); err != nil {
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		if request.DNS == nil {
			return helperprotocol.Response{OK: false, Error: "dns is required"}
		}
		s.Log.Printf(
			"updating split DNS for session %s: domains=%d",
			request.SessionID, len(request.DNS.Domains),
		)
		if err := s.updateSessionDNS(request.SessionID, *request.DNS); err != nil {
			s.Log.Printf("update split DNS for session %s: %v", request.SessionID, err)
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		s.Log.Printf("split DNS updated for session %s", request.SessionID)
		return helperprotocol.Response{OK: true, Version: Version, Protocol: helperprotocol.Version}
	case helperprotocol.OpReadLogs:
		if err := singbox.ValidateSessionID(request.SessionID); err != nil {
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		data, offset, err := s.readSessionLogs(request.SessionID, request.LogOffset)
		if err != nil {
			return helperprotocol.Response{OK: false, Error: err.Error()}
		}
		return helperprotocol.Response{
			OK: true, Version: Version, Protocol: helperprotocol.Version,
			LogData: data, LogOffset: offset,
		}
	default:
		return helperprotocol.Response{OK: false, Error: fmt.Sprintf("unsupported op %q", request.Op)}
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

func writeResponse(w io.Writer, response helperprotocol.Response) error {
	return json.NewEncoder(w).Encode(response)
}
