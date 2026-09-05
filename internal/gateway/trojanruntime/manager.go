// Package trojanruntime owns loopback sing-box processes for active Gateway
// control Sessions.
package trojanruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trojanws"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const defaultReadyTimeout = 10 * time.Second

type Config struct {
	BinaryPath   string
	LogLevel     string
	Logger       *slog.Logger
	ReadyTimeout time.Duration
}

type Manager struct {
	ctx    context.Context
	config Config
	mu     sync.Mutex
	items  map[tunnel.SessionToken]*sessionProcess
	closed bool
}

type sessionProcess struct {
	sessionID  string
	generation uint64
	namespace  string
	hash       string
	port       int
	references int
	cancel     context.CancelFunc
	done       chan struct{}
	errMu      sync.RWMutex
	waitErr    error
	directory  string
}

func NewManager(ctx context.Context, config Config) (*Manager, error) {
	if ctx == nil {
		return nil, errors.New("gateway Trojan runtime context is required")
	}
	if config.BinaryPath == "" {
		return nil, errors.New("gateway sing-box binary path is required")
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = defaultReadyTimeout
	}
	return &Manager{ctx: ctx, config: config, items: make(map[tunnel.SessionToken]*sessionProcess)}, nil
}

func (manager *Manager) Register(
	ctx context.Context,
	sessionID string,
	generation uint64,
	namespace string,
	networkSpecHash string,
	network networkspec.Spec,
) error {
	token, err := tunnel.RelaySessionToken(sessionID, generation)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return errors.New("gateway Trojan runtime is closed")
	}
	if current := manager.items[token]; current != nil {
		if current.namespace != namespace || current.hash != networkSpecHash {
			return errors.New("session forward authorization changed")
		}
		current.references++
		return nil
	}
	process, err := manager.startLocked(ctx, sessionID, generation, namespace, networkSpecHash, network)
	if err != nil {
		return err
	}
	manager.items[token] = process
	return nil
}

func (manager *Manager) startLocked(
	ctx context.Context,
	sessionID string,
	generation uint64,
	namespace string,
	networkSpecHash string,
	network networkspec.Spec,
) (*sessionProcess, error) {
	port, err := utils.FreeTCPPort()
	if err != nil {
		return nil, err
	}
	password, err := trojanws.DeriveSessionPassword(sessionID, generation)
	if err != nil {
		return nil, err
	}
	config, err := singbox.GenerateGatewaySessionConfig(singbox.GatewaySessionOptions{
		SessionID: sessionID, ListenPort: port, TrojanPassword: password,
		Network: singbox.NetworkSpec{
			PodCIDRs: network.PodCIDRs, PodIPs: network.PodIPs,
			ServiceCIDRs: network.ServiceCIDRs, ServiceIPs: network.ServiceIPs,
			DNSServer: network.DNSServer, ClusterDomains: network.ClusterDomains,
		},
		LogLevel: manager.config.LogLevel,
	})
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "kubeloop-gateway-trojan-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		cleanup()
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(directory, "sing-box.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		cleanup()
		return nil, err
	}
	processContext, cancel := context.WithCancel(manager.ctx)
	//nolint:gosec // BinaryPath is fixed by the Gateway deployment configuration.
	command := exec.CommandContext(processContext, manager.config.BinaryPath, "run", "-c", configPath)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		cleanup()
		return nil, err
	}
	done := make(chan struct{})
	process := &sessionProcess{
		sessionID: sessionID, generation: generation, namespace: namespace, hash: networkSpecHash,
		port: port, references: 1, cancel: cancel, done: done, directory: directory,
	}
	go func() {
		process.errMu.Lock()
		process.waitErr = command.Wait()
		process.errMu.Unlock()
		_ = logFile.Close()
		close(done)
	}()
	readyCtx, readyCancel := context.WithTimeout(ctx, manager.config.ReadyTimeout)
	defer readyCancel()
	for {
		connection, dialErr := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(
			readyCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		)
		if dialErr == nil {
			_ = connection.Close()
			return process, nil
		}
		select {
		case <-done:
			cancel()
			cleanup()
			return nil, fmt.Errorf("gateway sing-box exited before ready: %w", process.err())
		case <-readyCtx.Done():
			cancel()
			<-done
			cleanup()
			return nil, fmt.Errorf("wait for Gateway sing-box: %w", readyCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (manager *Manager) Release(sessionID string, generation uint64) {
	token, err := tunnel.RelaySessionToken(sessionID, generation)
	if err != nil {
		return
	}
	manager.mu.Lock()
	process := manager.items[token]
	if process == nil {
		manager.mu.Unlock()
		return
	}
	process.references--
	if process.references > 0 {
		manager.mu.Unlock()
		return
	}
	delete(manager.items, token)
	manager.mu.Unlock()
	manager.stop(process)
}

func (manager *Manager) ResolveTrojanSession(identity websocketmux.Identity) (*url.URL, error) {
	token, err := tunnel.RelaySessionToken(identity.SessionID, identity.SessionGeneration)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	process := manager.items[token]
	if process == nil || process.namespace != identity.Namespace || process.hash != identity.NetworkSpecHash {
		return nil, errors.New("authorized Session forward runtime is not active")
	}
	select {
	case <-process.done:
		return nil, fmt.Errorf("gateway sing-box exited: %w", process.err())
	default:
	}
	return url.Parse("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(process.port)))
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	processes := make([]*sessionProcess, 0, len(manager.items))
	for _, process := range manager.items {
		processes = append(processes, process)
	}
	clear(manager.items)
	manager.mu.Unlock()
	for _, process := range processes {
		manager.stop(process)
	}
	return nil
}

func (manager *Manager) stop(process *sessionProcess) {
	process.cancel()
	select {
	case <-process.done:
		err := process.err()
		if err != nil && manager.config.Logger != nil {
			manager.config.Logger.Debug("Gateway sing-box stopped", "session_id", process.sessionID, "error", err)
		}
	case <-time.After(5 * time.Second):
		if manager.config.Logger != nil {
			manager.config.Logger.Warn("timed out stopping Gateway sing-box", "session_id", process.sessionID)
		}
	}
	_ = os.RemoveAll(process.directory)
}

func (process *sessionProcess) err() error {
	process.errMu.RLock()
	defer process.errMu.RUnlock()
	return process.waitErr
}
