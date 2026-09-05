// Package forwardruntime runs the unprivileged client sing-box SOCKS to
// Trojan/WebSocket data path.
package forwardruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type Options struct {
	SessionID   string
	Generation  uint64
	Endpoint    string
	RelayTicket string
	TLSInsecure bool
	LogLevel    string
}

type Starter struct {
	BinaryPath   string
	ReadyTimeout time.Duration
}

type Process struct {
	address   string
	cancel    context.CancelFunc
	done      chan struct{}
	directory string
	logPath   string
	errMu     sync.RWMutex
	waitErr   error
	closeOnce sync.Once
}

func (starter Starter) Start(ctx context.Context, options Options) (*Process, error) {
	if strings.TrimSpace(starter.BinaryPath) == "" {
		return nil, errors.New("client sing-box binary path is required")
	}
	port, err := utils.FreeTCPPort()
	if err != nil {
		return nil, err
	}
	password, err := trojanPassword(options.SessionID, options.Generation)
	if err != nil {
		return nil, err
	}
	config, err := singbox.GenerateClientTrojanConfig(singbox.ClientTrojanOptions{
		SessionID: options.SessionID, ListenPort: port, Endpoint: options.Endpoint,
		RelayTicket: options.RelayTicket, TrojanPassword: password,
		TLSInsecure: options.TLSInsecure, LogLevel: options.LogLevel,
	})
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "kubeloop-client-trojan-")
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	logPath := filepath.Join(directory, "sing-box.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	processCtx, cancel := context.WithCancel(ctx)
	//nolint:gosec // BinaryPath is resolved from the signed bundled component, not user input.
	command := exec.CommandContext(processCtx, starter.BinaryPath, "run", "-c", configPath)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		_ = os.RemoveAll(directory)
		return nil, err
	}
	process := &Process{
		address: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), cancel: cancel,
		done: make(chan struct{}), directory: directory, logPath: logPath,
	}
	go func() {
		process.errMu.Lock()
		process.waitErr = command.Wait()
		process.errMu.Unlock()
		_ = logFile.Close()
		close(process.done)
	}()
	timeout := starter.ReadyTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, timeout)
	defer readyCancel()
	for {
		connection, dialErr := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(
			readyCtx, "tcp", process.address,
		)
		if dialErr == nil {
			_ = connection.Close()
			return process, nil
		}
		select {
		case <-process.done:
			startupLog := readLogTail(process.logPath, options.RelayTicket)
			processErr := process.Err()
			_ = process.Close()
			if startupLog != "" {
				return nil, fmt.Errorf("client sing-box exited before ready: %w: %s", processErr, startupLog)
			}
			return nil, fmt.Errorf("client sing-box exited before ready: %w", processErr)
		case <-readyCtx.Done():
			_ = process.Close()
			return nil, fmt.Errorf("wait for client sing-box: %w", readyCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func readLogTail(path, secret string) string {
	const maximumLogBytes = 8 << 10
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	offset := int64(0)
	if size > maximumLogBytes {
		offset = size - maximumLogBytes
		size = maximumLogBytes
	}
	buffer := make([]byte, int(size))
	read, err := file.ReadAt(buffer, offset)
	if err != nil && read == 0 {
		return ""
	}
	output := strings.TrimSpace(string(buffer[:read]))
	if secret != "" {
		output = strings.ReplaceAll(output, secret, "[REDACTED]")
	}
	return output
}

func (process *Process) Address() string       { return process.address }
func (process *Process) Done() <-chan struct{} { return process.done }
func (process *Process) Err() error {
	process.errMu.RLock()
	defer process.errMu.RUnlock()
	return process.waitErr
}

func (process *Process) Close() error {
	process.closeOnce.Do(func() {
		process.cancel()
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
		}
		_ = os.RemoveAll(process.directory)
	})
	return nil
}
