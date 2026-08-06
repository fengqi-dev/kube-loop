package helper

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxSessionLogRead = 256 << 10

func (s *Server) readSessionLogs(sessionID string, offset int64) (string, int64, error) {
	if offset < 0 {
		return "", 0, fmt.Errorf("log offset must not be negative")
	}
	s.mu.Lock()
	current := s.sessions[sessionID]
	if current == nil {
		s.mu.Unlock()
		return "", offset, fmt.Errorf("session not found")
	}
	logPath := filepath.Join(current.workDir, "sing-box.log")
	s.mu.Unlock()

	file, err := os.Open(logPath)
	if err != nil {
		return "", offset, fmt.Errorf("open sing-box log: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", offset, fmt.Errorf("stat sing-box log: %w", err)
	}
	if offset > info.Size() {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", offset, fmt.Errorf("seek sing-box log: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxSessionLogRead))
	if err != nil {
		return "", offset, fmt.Errorf("read sing-box log: %w", err)
	}
	return string(content), offset + int64(len(content)), nil
}
