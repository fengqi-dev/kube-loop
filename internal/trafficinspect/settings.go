package trafficinspect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	"github.com/fengqi-dev/kube-loop/internal/userpaths"
)

const (
	settingsVersion      = 1
	maximumSettingsBytes = 64 << 10
)

type Settings struct {
	Enabled bool `json:"enabled"`
}

type persistedSettings struct {
	Version int  `json:"version"`
	Enabled bool `json:"enabled"`
}

type SettingsStore struct {
	path string
}

func NewSettingsStore(path string) (*SettingsStore, error) {
	if strings.TrimSpace(path) == "" {
		layout, err := userpaths.Default()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(layout.ConfigDir(), "traffic-inspection.json")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve traffic inspection settings path: %w", err)
	}
	return &SettingsStore{path: absolute}, nil
}

func (s *SettingsStore) Load(fallback Settings) (Settings, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("open traffic inspection settings: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumSettingsBytes+1))
	if err != nil {
		return Settings{}, errors.New("read traffic inspection settings")
	}
	if len(raw) > maximumSettingsBytes {
		return Settings{}, errors.New("traffic inspection settings exceed 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var persisted persistedSettings
	if err := decoder.Decode(&persisted); err != nil {
		return Settings{}, errors.New("decode traffic inspection settings")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Settings{}, errors.New("traffic inspection settings must contain one JSON document")
	}
	if persisted.Version != settingsVersion {
		return Settings{}, fmt.Errorf("unsupported traffic inspection settings version %d", persisted.Version)
	}
	return Settings{Enabled: persisted.Enabled}, nil
}

func (s *SettingsStore) Save(settings Settings) error {
	raw, err := json.MarshalIndent(persistedSettings{
		Version: settingsVersion,
		Enabled: settings.Enabled,
	}, "", "  ")
	if err != nil {
		return errors.New("encode traffic inspection settings")
	}
	raw = append(raw, '\n')
	if err := fsatomic.WriteFile(s.path, raw, 0o700, 0o600); err != nil {
		return fmt.Errorf("save traffic inspection settings: %w", err)
	}
	return nil
}
