package migration

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

const (
	legacyStateName = "state.json"
	backupName      = "state-v1.backup.json"
	markerName      = "migration-v2.json"
	maxLegacyBytes  = 8 << 20
)

type Status struct {
	LegacyDetected bool   `json:"legacyDetected"`
	BackupPath     string `json:"backupPath,omitempty"`
	CompletedAt    string `json:"completedAt,omitempty"`
	Error          string `json:"error,omitempty"`
}

type marker struct {
	SchemaVersion int    `json:"schemaVersion"`
	BackupFile    string `json:"backupFile"`
	CompletedAt   string `json:"completedAt"`
}

// PreserveLegacyState creates a byte-for-byte backup without decoding legacy
// state. In particular, it never follows or opens kubeconfig paths referenced
// by V1 and never restores V1 Kubernetes resource intents.
func PreserveLegacyState(root string, now func() time.Time) (Status, error) {
	if root == "" {
		return Status{}, nil
	}
	if now == nil {
		now = time.Now
	}
	legacyPath := filepath.Join(root, legacyStateName)
	info, err := os.Lstat(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	}
	if err != nil {
		return Status{LegacyDetected: true}, fmt.Errorf("inspect V1 state: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Status{LegacyDetected: true}, errors.New("V1 state must be a regular file and not a symbolic link")
	}
	if info.Size() > maxLegacyBytes {
		return Status{LegacyDetected: true}, errors.New("V1 state exceeds the 8 MiB backup limit")
	}

	backupPath := filepath.Join(root, backupName)
	markerPath := filepath.Join(root, markerName)
	if existing, markerErr := readMarker(markerPath); markerErr == nil {
		if _, backupErr := readBoundedRegularFile(backupPath, maxLegacyBytes); backupErr != nil {
			return Status{LegacyDetected: true, BackupPath: backupPath}, fmt.Errorf("validate marked V1 state backup: %w", backupErr)
		}
		return Status{LegacyDetected: true, BackupPath: backupPath, CompletedAt: existing.CompletedAt}, nil
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return Status{LegacyDetected: true, BackupPath: backupPath}, markerErr
	}

	if _, err := os.Lstat(backupPath); errors.Is(err, os.ErrNotExist) {
		raw, readErr := readBoundedRegularFile(legacyPath, maxLegacyBytes)
		if readErr != nil {
			return Status{LegacyDetected: true}, fmt.Errorf("read V1 state for backup: %w", readErr)
		}
		if writeErr := fsatomic.WriteFile(backupPath, raw, 0o700, 0o600); writeErr != nil {
			return Status{LegacyDetected: true}, fmt.Errorf("write V1 state backup: %w", writeErr)
		}
	} else if err != nil {
		return Status{LegacyDetected: true}, fmt.Errorf("inspect V1 state backup: %w", err)
	} else if backupInfo, statErr := os.Lstat(backupPath); statErr != nil || backupInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.Mode().IsRegular() {
		return Status{LegacyDetected: true}, errors.New("V1 state backup must be a regular file and not a symbolic link")
	}

	completedAt := now().UTC().Format(time.RFC3339Nano)
	rawMarker, err := json.MarshalIndent(marker{SchemaVersion: 1, BackupFile: backupName, CompletedAt: completedAt}, "", "  ")
	if err != nil {
		return Status{LegacyDetected: true, BackupPath: backupPath}, errors.New("encode V2 migration marker")
	}
	rawMarker = append(rawMarker, '\n')
	if err := fsatomic.WriteFile(markerPath, rawMarker, 0o700, 0o600); err != nil {
		return Status{LegacyDetected: true, BackupPath: backupPath}, fmt.Errorf("write V2 migration marker: %w", err)
	}
	return Status{LegacyDetected: true, BackupPath: backupPath, CompletedAt: completedAt}, nil
}

func readMarker(path string) (marker, error) {
	raw, err := readBoundedRegularFile(path, 16<<10)
	if err != nil {
		return marker{}, err
	}
	var value marker
	if err := json.Unmarshal(raw, &value); err != nil || value.SchemaVersion != 1 || value.BackupFile != backupName || value.CompletedAt == "" {
		return marker{}, errors.New("V2 migration marker is invalid")
	}
	return value, nil
}

func readBoundedRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("file must be regular and not a symbolic link")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return raw, nil
}
