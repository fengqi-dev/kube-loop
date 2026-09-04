package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const appSettingsFileName = "settings.json"

// appSettings is the persisted global application settings document. Unknown
// fields are preserved so future settings survive round trips.
type appSettings struct {
	LogLevel string `json:"logLevel,omitempty"`
}

// settingsStore persists the global application settings to the KubeLoop
// config directory. It is a small, atomic JSON document.
type settingsStore struct {
	path string
	data appSettings
}

func newSettingsStore(layout utils.Layout) *settingsStore {
	return &settingsStore{path: filepath.Join(layout.ConfigDir(), appSettingsFileName)}
}

func (store *settingsStore) load() error {
	content, err := os.ReadFile(store.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read application settings: %w", err)
	}
	var data appSettings
	if err := json.Unmarshal(content, &data); err != nil {
		return fmt.Errorf("decode application settings: %w", err)
	}
	store.data = data
	return nil
}

// logLevel returns the persisted log level, defaulting to info.
func (store *settingsStore) logLevel() (slog.Level, error) {
	return parseSlogLevel(store.data.LogLevel)
}

// setLogLevel updates and persists the log level.
func (store *settingsStore) setLogLevel(level string) error {
	if _, err := parseSlogLevel(level); err != nil {
		return err
	}
	normalized := slogLevelString(parseOrInfo(level))
	updated := store.data
	updated.LogLevel = normalized
	if err := utils.WriteFile(store.path, mustJSON(updated), 0o700, 0o600); err != nil {
		return fmt.Errorf("persist application settings: %w", err)
	}
	store.data = updated
	return nil
}

func parseOrInfo(raw string) slog.Level {
	level, err := parseSlogLevel(raw)
	if err != nil {
		return slog.LevelInfo
	}
	return level
}

func mustJSON(value appSettings) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return data
}

func (a *App) Close() {
	if a.logSink != nil {
		a.logSink.close()
	}
}

// loadPersistedLogLevel applies the saved log level to the running sink. It is
// best effort: a corrupt or missing settings file falls back to info.
func (a *App) loadPersistedLogLevel() {
	if a.settings == nil || a.logSink == nil {
		return
	}
	if err := a.settings.load(); err != nil {
		a.logWarn("Load application settings: " + err.Error())
	}
	level, err := a.settings.logLevel()
	if err != nil {
		a.logWarn("Apply saved log level: " + err.Error())
		return
	}
	a.logSink.level.Set(level)
}

// GetLogLevel returns the currently configured log level (debug/info/warn/error).
func (a *App) GetLogLevel() (string, error) {
	if a.logSink == nil {
		return "info", nil
	}
	return slogLevelString(a.logSink.Level()), nil
}

// currentLogLevel returns the active log threshold as its canonical lowercase
// label, defaulting to info. It is used to configure the sing-box runtime.
func (a *App) currentLogLevel() string {
	if a.logSink == nil {
		return "info"
	}
	return slogLevelString(a.logSink.Level())
}

// SetLogLevel updates the log level threshold and persists it. It returns the
// normalized level so the caller can display the canonical value.
func (a *App) SetLogLevel(level string) (string, error) {
	parsed, err := parseSlogLevel(level)
	if err != nil {
		return "", err
	}
	normalized := slogLevelString(parsed)
	if a.logSink != nil {
		a.logSink.level.Set(parsed)
	}
	if a.settings != nil {
		if err := a.settings.setLogLevel(normalized); err != nil {
			return "", err
		}
	}
	a.logInfo("Application log level set to " + normalized)
	return normalized, nil
}
