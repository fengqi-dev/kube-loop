package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	helperplatform "github.com/fengqi-dev/kube-loop/internal/helper/platform"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

// Server is the privileged helper RPC server.

func (s *Server) updateSessionDNS(sessionID string, dns singbox.DNSMeta) error {
	if dns.Listen == "" || dns.Port < 1 || dns.Port > 65535 {
		return fmt.Errorf("invalid DNS listen address")
	}
	if len(dns.Domains) == 0 {
		return fmt.Errorf("DNS domains are required")
	}
	s.mu.Lock()
	current := s.sessions[sessionID]
	s.mu.Unlock()
	if current == nil {
		return fmt.Errorf("session %s is not active", sessionID)
	}
	current.lifecycleMu.Lock()
	defer current.lifecycleMu.Unlock()
	s.mu.Lock()
	active := s.sessions[sessionID] == current
	s.mu.Unlock()
	if !active || current.stopping {
		return fmt.Errorf("session %s is not active", sessionID)
	}
	if current.workDir == "" {
		return fmt.Errorf("session work directory is unavailable")
	}
	previous := current.dns
	_ = helperplatform.RestoreLinkDNS(current.tunAddress)
	_ = helperplatform.RestoreDNS(current.workDir, previous)
	if err := helperplatform.ApplyDNS(current.workDir, dns); err != nil {
		_ = helperplatform.ApplyDNS(current.workDir, previous)
		_ = helperplatform.ApplyLinkDNS(current.tunAddress, previous)
		return fmt.Errorf("install split DNS: %w", err)
	}
	if err := helperplatform.ApplyLinkDNS(current.tunAddress, dns); err != nil {
		// Drop-in may still be enough for FQDN; keep going so SetDNSNamespace
		// is not blocked by a transient missing TUN iface.
		s.Log.Printf("link DNS update for %s: %v", sessionID, err)
	}
	meta, err := json.Marshal(dns)
	if err != nil {
		_ = helperplatform.RestoreDNS(current.workDir, dns)
		_ = helperplatform.ApplyDNS(current.workDir, previous)
		_ = helperplatform.ApplyLinkDNS(current.tunAddress, previous)
		return fmt.Errorf("encode DNS metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(current.workDir, "dns-meta.json"), meta, 0o600); err != nil {
		_ = helperplatform.RestoreDNS(current.workDir, dns)
		_ = helperplatform.ApplyDNS(current.workDir, previous)
		_ = helperplatform.ApplyLinkDNS(current.tunAddress, previous)
		return fmt.Errorf("write DNS metadata: %w", err)
	}
	current.dns = dns
	return nil
}

func (s *Server) stopSession(sessionID string) error {
	s.mu.Lock()
	current := s.sessions[sessionID]
	if current == nil {
		s.mu.Unlock()
		return nil
	}
	cmd := current.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("session is still starting")
	}
	if err := helperplatform.StopManagedProcess(cmd.Process); err != nil {
		s.Log.Printf("graceful stop for session %s failed: %v", sessionID, err)
		_ = cmd.Process.Kill()
	}
	select {
	case <-current.done:
		return nil
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-current.done:
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("timed out waiting for privileged session to stop")
		}
	}
}

func (s *Server) stopAllSessions() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.stopSession(id); err != nil {
			s.Log.Printf("stop privileged session %s during stop-all: %v", id, err)
		}
	}
}
