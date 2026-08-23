package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
)

var clientAPIErrorForTest = clientremote.APIError{
	Status: 403, Code: "forbidden", Message: "denied", RequestID: "request-1",
}

func TestDefaultConfigPathUsesConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".kubeloop", "config", "mcp.json"); path != want {
		t.Fatalf("DefaultConfigPath() = %q, want %q", path, want)
	}
}

func TestKeyringServiceSeparatesReleaseAndDevelopment(t *testing.T) {
	t.Parallel()
	if got := keyringServiceForVersion("v2.1.0"); got != keyringService {
		t.Fatalf("release service = %q, want %q", got, keyringService)
	}
	if got := keyringServiceForVersion("dev"); got != devKeyringService {
		t.Fatalf("development service = %q, want %q", got, devKeyringService)
	}
}

type memorySecrets struct{ values map[string]string }

func (secrets *memorySecrets) Set(service, account, secret string) error {
	if secrets.values == nil {
		secrets.values = make(map[string]string)
	}
	secrets.values[service+"\x00"+account] = secret
	return nil
}
func (secrets *memorySecrets) Get(service, account string) (string, error) {
	value, found := secrets.values[service+"\x00"+account]
	if !found {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (secrets *memorySecrets) Delete(service, account string) error {
	key := service + "\x00" + account
	if _, found := secrets.values[key]; !found {
		return keyring.ErrNotFound
	}
	delete(secrets.values, key)
	return nil
}

func TestSystemConfigStoreNeverWritesTokenToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	secrets := &memorySecrets{}
	store, err := newSystemConfigStore(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	if err := store.Save(Config{Enabled: true, Port: 31999, TokenEnabled: true, Token: token}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "token\"") {
		t.Fatalf("secret leaked to settings file: %s", raw)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != token || loaded.Port != 31999 || !loaded.Enabled || !loaded.TokenEnabled {
		t.Fatalf("loaded=%#v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("settings mode=%o", info.Mode().Perm())
	}
}

func TestSystemConfigStoreDefaultsWithoutTouchingKeyring(t *testing.T) {
	store, err := newSystemConfigStore(filepath.Join(t.TempDir(), "missing.json"), &memorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.Port != DefaultPort || !config.TokenEnabled || config.Enabled {
		t.Fatalf("config=%#v", config)
	}
}

func TestSystemConfigStoreRestoresTokenWhenSettingsWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	secrets := &memorySecrets{}
	store, err := newSystemConfigStore(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := strings.Repeat("a", 64)
	oldConfig := Config{Enabled: true, Port: 31999, TokenEnabled: true, Token: oldToken}
	if err := store.Save(oldConfig); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("disk unavailable")
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		if path == store.path {
			return writeErr
		}
		return fsatomic.WriteFile(path, raw, dirMode, fileMode)
	}
	newToken := strings.Repeat("b", 64)
	if err := store.Save(Config{
		Enabled: true, Port: 32000, TokenEnabled: true, Token: newToken,
	}); !errors.Is(err, writeErr) {
		t.Fatalf("Save error = %v", err)
	}
	storedToken, err := secrets.Get(store.service, keyringTokenAccount)
	if err != nil || storedToken != oldToken {
		t.Fatalf("restored token = %q, %v", storedToken, err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != oldToken || loaded.Port != oldConfig.Port {
		t.Fatalf("loaded config after rollback = %#v", loaded)
	}
}

func TestSystemConfigStoreRemovesNewTokenWhenInitialWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	secrets := &memorySecrets{}
	store, err := newSystemConfigStore(path, secrets)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("disk unavailable")
	store.writeFile = func(string, []byte, os.FileMode, os.FileMode) error { return writeErr }
	if err := store.Save(Config{
		Enabled: true, Port: 31999, TokenEnabled: true, Token: strings.Repeat("a", 64),
	}); !errors.Is(err, writeErr) {
		t.Fatalf("Save error = %v", err)
	}
	if _, err := secrets.Get(store.service, keyringTokenAccount); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("uncommitted token remained in keyring: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed initial save created settings file: %v", err)
	}
}

func TestStableControlPlaneErrorMapping(t *testing.T) {
	err := stableError(&clientAPIErrorForTest)
	var toolError *ToolError
	if !errors.As(err, &toolError) || toolError.Code != ErrorForbidden || toolError.RequestID != "request-1" {
		t.Fatalf("error=%#v", err)
	}
}
