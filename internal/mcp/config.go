package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	"github.com/fengqi-dev/kube-loop/internal/userpaths"
)

const (
	DefaultPort         = 30808
	configVersion       = 1
	maximumConfigBytes  = 64 << 10
	keyringService      = "KubeLoop"
	devKeyringService   = "KubeLoop Dev"
	keyringTokenAccount = "mcp:bearer-token" //nolint:gosec // Keyring account label, not credential material.
)

// Config is the runtime MCP configuration. Token is never serialized into the
// settings file; SystemConfigStore keeps it in the OS credential vault.
type Config struct {
	Enabled      bool
	Port         int
	TokenEnabled bool
	Token        string
}

func DefaultConfig() Config {
	return Config{Port: DefaultPort, TokenEnabled: true}
}

type ConfigStore interface {
	Load() (Config, error)
	Save(Config) error
}

type SecretBackend interface {
	Set(service, account, secret string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

type keyringBackend struct{}

func (keyringBackend) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (keyringBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (keyringBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type persistedConfig struct {
	Version      int  `json:"version"`
	Enabled      bool `json:"enabled"`
	Port         int  `json:"port"`
	TokenEnabled bool `json:"tokenEnabled"`
}

// SystemConfigStore splits non-secret settings from the MCP bearer token.
type SystemConfigStore struct {
	path      string
	secrets   SecretBackend
	service   string
	mu        sync.Mutex
	writeFile func(string, []byte, os.FileMode, os.FileMode) error
}

func DefaultConfigPath() (string, error) {
	layout, err := userpaths.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.ConfigDir(), "mcp.json"), nil
}

func NewSystemConfigStoreForVersion(path, version string) (*SystemConfigStore, error) {
	return newSystemConfigStoreWithService(path, keyringBackend{}, keyringServiceForVersion(version))
}

func keyringServiceForVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return devKeyringService
	}
	return keyringService
}

func newSystemConfigStore(path string, secrets SecretBackend) (*SystemConfigStore, error) {
	return newSystemConfigStoreWithService(path, secrets, keyringService)
}

func newSystemConfigStoreWithService(path string, secrets SecretBackend, service string) (*SystemConfigStore, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve MCP settings path")
	}
	if secrets == nil {
		return nil, errors.New("MCP secret store is required")
	}
	return &SystemConfigStore{
		path: absolute, secrets: secrets, service: service, writeFile: fsatomic.WriteFile,
	}, nil
}

func (store *SystemConfigStore) Path() string { return store.path }

func (store *SystemConfigStore) Load() (Config, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	settings, err := readPersistedConfig(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	config := Config{
		Enabled: settings.Enabled, Port: settings.Port, TokenEnabled: settings.TokenEnabled,
	}
	if !config.TokenEnabled {
		return config, nil
	}
	token, err := store.secrets.Get(store.service, keyringTokenAccount)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return config, nil
		}
		return Config{}, fmt.Errorf("read MCP token from system keyring: %w", err)
	}
	config.Token = token
	return config, nil
}

func (store *SystemConfigStore) Save(config Config) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	config, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	previousToken := ""
	hadPreviousToken := false
	if config.Token != "" {
		previousToken, err = store.secrets.Get(store.service, keyringTokenAccount)
		if err == nil {
			hadPreviousToken = true
		} else if !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("read existing MCP token from system keyring: %w", err)
		}
		if err := store.secrets.Set(store.service, keyringTokenAccount, config.Token); err != nil {
			return fmt.Errorf("store MCP token in system keyring: %w", err)
		}
	}
	raw, err := json.MarshalIndent(persistedConfig{
		Version: configVersion, Enabled: config.Enabled, Port: config.Port,
		TokenEnabled: config.TokenEnabled,
	}, "", "  ")
	if err != nil {
		return errors.New("encode MCP settings")
	}
	raw = append(raw, '\n')
	writeFile := store.writeFile
	if writeFile == nil {
		writeFile = fsatomic.WriteFile
	}
	if err := writeFile(store.path, raw, 0o700, 0o600); err != nil {
		saveErr := fmt.Errorf("save MCP settings: %w", err)
		if config.Token == "" {
			return saveErr
		}
		rollbackErr := store.restoreToken(previousToken, hadPreviousToken)
		return errors.Join(saveErr, rollbackErr)
	}
	return nil
}

func (store *SystemConfigStore) restoreToken(previous string, existed bool) error {
	if existed {
		if err := store.secrets.Set(store.service, keyringTokenAccount, previous); err != nil {
			return fmt.Errorf("restore MCP token in system keyring: %w", err)
		}
		return nil
	}
	if err := store.secrets.Delete(store.service, keyringTokenAccount); err != nil &&
		!errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("remove uncommitted MCP token from system keyring: %w", err)
	}
	return nil
}

func readPersistedConfig(path string) (persistedConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return persistedConfig{}, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maximumConfigBytes+1))
	if err != nil {
		return persistedConfig{}, errors.New("read MCP settings")
	}
	if len(raw) > maximumConfigBytes {
		return persistedConfig{}, errors.New("MCP settings exceed 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var settings persistedConfig
	if err := decoder.Decode(&settings); err != nil {
		return persistedConfig{}, errors.New("decode MCP settings")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return persistedConfig{}, errors.New("MCP settings must contain one JSON document")
	}
	if settings.Version != configVersion {
		return persistedConfig{}, fmt.Errorf("unsupported MCP settings version %d", settings.Version)
	}
	if settings.Port <= 0 || settings.Port > 65535 {
		return persistedConfig{}, errors.New("MCP settings contain an invalid port")
	}
	return settings, nil
}

func normalizeConfig(config Config) (Config, error) {
	if config.Port == 0 {
		config.Port = DefaultPort
	}
	if config.Port < 1 || config.Port > 65535 {
		return Config{}, fmt.Errorf("invalid MCP port %d", config.Port)
	}
	config.Token = strings.TrimSpace(config.Token)
	if config.Token != "" && len(config.Token) < 32 {
		return Config{}, errors.New("MCP token is invalid")
	}
	return config, nil
}
