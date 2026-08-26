//go:build darwin

package supervisor

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

type Server struct {
	config  Config
	auth    Auth
	updater *Updater
	log     *log.Logger
}

func NewServer(config Config, auth Auth, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		config:  config,
		auth:    auth,
		updater: NewUpdater(config, launchdWorker{config: config}, auth.UID),
		log:     logger,
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if err := s.updater.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted worker update: %w", err)
	}
	//nolint:gosec // The socket directory must be traversable; the socket itself is mode 0600.
	if err := os.MkdirAll(filepath.Dir(s.config.SocketPath), 0o755); err != nil {
		return fmt.Errorf("create supervisor socket directory: %w", err)
	}
	_ = os.Remove(s.config.SocketPath)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: s.config.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen supervisor socket: %w", err)
	}
	defer func() { _ = listener.Close() }()
	defer func() { _ = os.Remove(s.config.SocketPath) }()
	if err := os.Chown(s.config.SocketPath, s.auth.UID, 0); err != nil {
		return fmt.Errorf("set supervisor socket owner: %w", err)
	}
	if err := os.Chmod(s.config.SocketPath, 0o600); err != nil {
		return fmt.Errorf("set supervisor socket mode: %w", err)
	}

	var handlers sync.WaitGroup
	stopListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopListener()
	defer handlers.Wait()
	s.log.Printf("kubeloop-supervisor listening on %s (protocol %d)", s.config.SocketPath, supervisor.Version)
	for {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept supervisor connection: %w", acceptErr)
		}
		handlers.Go(func() { s.handle(ctx, connection) })
	}
}

func (s *Server) handle(serverCtx context.Context, connection *net.UnixConn) {
	defer func() { _ = connection.Close() }()
	stopClose := context.AfterFunc(serverCtx, func() { _ = connection.Close() })
	defer stopClose()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Minute))
	peerUID, err := unixPeerUID(connection)
	if err != nil {
		s.writeError(connection, "unable to verify peer credentials")
		return
	}
	var request supervisor.Request
	if err := supervisor.ReadFrame(connection, &request, supervisor.MaxRequestBytes); err != nil {
		s.writeError(connection, "invalid request")
		return
	}
	if request.Protocol != supervisor.Version {
		s.writeError(connection, "unsupported supervisor protocol")
		return
	}
	if !s.auth.Authorize(request.Token, peerUID) {
		s.writeError(connection, "unauthorized")
		return
	}

	ctx, cancel := context.WithTimeout(serverCtx, 2*time.Minute)
	defer cancel()
	response := supervisor.Response{Protocol: supervisor.Version, Channel: s.config.Channel}
	switch request.Op {
	case supervisor.OpStatus:
		if request.Manifest != nil {
			response.Error = "status does not accept a manifest"
			break
		}
		response.Worker, _ = s.updater.Status(ctx)
		response.PreviousAvailable = fileExists(s.config.PreviousPath())
		// A reachable supervisor remains healthy even when its worker is down;
		// callers must still be able to submit a recovery update.
		response.OK = true
	case supervisor.OpUpdateWorker:
		if request.Manifest == nil {
			response.Error = "update manifest is required"
			break
		}
		response = s.updater.Update(ctx, *request.Manifest, connection)
	case supervisor.OpRollbackWorker:
		if request.Manifest != nil {
			response.Error = "rollback does not accept a manifest"
			break
		}
		response = s.updater.Rollback(ctx)
	case supervisor.OpRestartWorker:
		if request.Manifest != nil {
			response.Error = "restart does not accept a manifest"
			break
		}
		response = s.updater.Restart(ctx)
	default:
		response.Error = fmt.Sprintf("unsupported operation %q", request.Op)
	}
	if err := supervisor.WriteFrame(connection, response, supervisor.MaxResponseBytes); err != nil {
		s.log.Printf("write supervisor response: %v", err)
	}
}

func (s *Server) writeError(w io.Writer, message string) {
	_ = supervisor.WriteFrame(w, supervisor.Response{
		Protocol: supervisor.Version, Channel: s.config.Channel, Error: message,
	}, supervisor.MaxResponseBytes)
}

func unixPeerUID(connection *net.UnixConn) (int, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	uid := -1
	var credentialErr error
	if err := raw.Control(func(fd uintptr) {
		credential, getErr := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if getErr != nil {
			credentialErr = getErr
			return
		}
		uid = int(credential.Uid)
	}); err != nil {
		return 0, err
	}
	if credentialErr != nil {
		return 0, credentialErr
	}
	return uid, nil
}
