package helper

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	helperplatform "github.com/fengqi-dev/kube-loop/internal/helper/platform"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

// Server is the privileged helper RPC server.

func (s *Server) startSession(spec singbox.SessionSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}
	config, err := spec.GenerateConfig()
	if err != nil {
		return fmt.Errorf("generate sing-box config: %w", err)
	}
	dns, err := spec.DNS()
	if err != nil {
		return fmt.Errorf("build DNS settings: %w", err)
	}
	routes, err := spec.Routes()
	if err != nil {
		return fmt.Errorf("build route settings: %w", err)
	}

	current := &session{
		done: make(chan struct{}), exited: make(chan sessionExit, 1),
		routes: routes, dns: dns, tunAddress: spec.TUNAddress,
	}
	s.mu.Lock()
	if existing := s.sessions[spec.ID]; existing != nil {
		s.mu.Unlock()
		return nil
	}
	stale := len(s.sessions) != 0
	s.mu.Unlock()
	// Only one privileged TUN is supported. Replace leftovers left behind by
	// crash/reload so reconnect does not fail until a manual helper stop.
	if stale {
		s.Log.Printf("replacing leftover privileged TUN session before starting %s", spec.ID)
		s.stopAllSessions()
	}
	s.mu.Lock()
	if len(s.sessions) != 0 {
		s.mu.Unlock()
		return fmt.Errorf("another privileged TUN session is already active")
	}
	s.sessions[spec.ID] = current
	s.mu.Unlock()
	fail := func() {
		s.mu.Lock()
		delete(s.sessions, spec.ID)
		s.mu.Unlock()
	}

	sessionRoot := filepath.Join(SystemStateDir(), "sessions")
	if err := os.MkdirAll(sessionRoot, 0o700); err != nil {
		fail()
		return fmt.Errorf("create protected session root: %w", err)
	}
	current.workDir = filepath.Join(sessionRoot, spec.ID)
	if err := os.RemoveAll(current.workDir); err != nil {
		fail()
		return fmt.Errorf("clear stale protected session: %w", err)
	}
	if err := os.Mkdir(current.workDir, 0o700); err != nil {
		fail()
		return fmt.Errorf("create protected session: %w", err)
	}
	cleanupFiles := func() { _ = os.RemoveAll(current.workDir) }
	if err := os.WriteFile(filepath.Join(current.workDir, "config.json"), config, 0o600); err != nil {
		cleanupFiles()
		fail()
		return fmt.Errorf("write protected sing-box config: %w", err)
	}

	binaryPath, err := resolveSingBoxPath(s.Auth)
	if err != nil {
		cleanupFiles()
		fail()
		return fmt.Errorf("ensure trusted sing-box core: %w", err)
	}
	logFile, err := os.OpenFile(
		filepath.Join(current.workDir, "sing-box.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600,
	)
	if err != nil {
		cleanupFiles()
		fail()
		return fmt.Errorf("open protected session log: %w", err)
	}
	if err := helperplatform.ApplyDNS(current.workDir, dns); err != nil {
		_ = helperplatform.RestoreDNS(current.workDir, dns)
		_ = logFile.Close()
		cleanupFiles()
		fail()
		return fmt.Errorf("install split DNS: %w", err)
	}

	cmd := exec.Command(
		binaryPath, "run", "-c", filepath.Join(current.workDir, "config.json"),
		"-D", current.workDir,
	)
	cmd.Dir = current.workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = helperplatform.RestoreDNS(current.workDir, dns)
		_ = logFile.Close()
		cleanupFiles()
		fail()
		return fmt.Errorf("start trusted sing-box core: %w", err)
	}
	s.mu.Lock()
	current.cmd = cmd
	s.mu.Unlock()
	go func() {
		waitErr := cmd.Wait()
		_ = logFile.Sync()
		logContent, _ := os.ReadFile(filepath.Join(current.workDir, "sing-box.log"))
		current.exited <- sessionExit{err: waitErr, log: tailText(logContent, 8<<10)}
		if waitErr != nil {
			s.Log.Printf("sing-box session %s exited: %v", spec.ID, waitErr)
		}
		current.lifecycleMu.Lock()
		current.stopping = true
		if err := helperplatform.RestoreLinkDNS(current.tunAddress); err != nil {
			s.Log.Printf("restore link DNS for session %s: %v", spec.ID, err)
		}
		if err := helperplatform.RestoreDNS(current.workDir, current.dns); err != nil {
			s.Log.Printf("restore platform DNS for session %s: %v", spec.ID, err)
		}
		helperplatform.CleanupRoutes(current.routes)
		if err := logFile.Close(); err != nil {
			s.Log.Printf("close log for session %s: %v", spec.ID, err)
		}
		if err := os.RemoveAll(current.workDir); err != nil {
			s.Log.Printf("remove protected files for session %s: %v", spec.ID, err)
		}
		s.mu.Lock()
		if s.sessions[spec.ID] == current {
			delete(s.sessions, spec.ID)
		}
		s.mu.Unlock()
		close(current.done)
		current.lifecycleMu.Unlock()
	}()
	controller := net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.ControllerPort))
	deadline := time.Now().Add(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	linkDNSApplied := false
	for {
		select {
		case result := <-current.exited:
			detail := strings.TrimSpace(result.log)
			if detail == "" {
				detail = "sing-box produced no diagnostic output"
			}
			if result.err != nil {
				return fmt.Errorf("sing-box exited during startup: %w: %s", result.err, detail)
			}
			return fmt.Errorf("sing-box exited during startup: %s", detail)
		case <-ticker.C:
			if !linkDNSApplied && helperplatform.ApplyLinkDNS(spec.TUNAddress, dns) == nil {
				linkDNSApplied = true
			}
			if controllerReady(controller, spec.ControllerSecret) {
				if !linkDNSApplied {
					_ = helperplatform.ApplyLinkDNS(spec.TUNAddress, dns)
				}
				return nil
			}
			if time.Now().After(deadline) {
				// Process is still alive; let the desktop-side waitReady continue.
				if !linkDNSApplied {
					_ = helperplatform.ApplyLinkDNS(spec.TUNAddress, dns)
				}
				return nil
			}
		}
	}
}

func controllerReady(address, secret string) bool {
	client := &http.Client{Timeout: 200 * time.Millisecond}
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/", nil)
	if err != nil {
		return false
	}
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNotFound
}

func tailText(content []byte, limit int) string {
	if limit <= 0 || len(content) <= limit {
		return string(content)
	}
	return string(content[len(content)-limit:])
}
