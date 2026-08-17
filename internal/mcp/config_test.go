package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/zalando/go-keyring"
)

var clientAPIErrorForTest = clientremote.APIError{
	Status: 403, Code: "forbidden", Message: "denied", RequestID: "request-1",
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

func TestStableControlPlaneErrorMapping(t *testing.T) {
	err := stableError(&clientAPIErrorForTest)
	var toolError *ToolError
	if !errors.As(err, &toolError) || toolError.Code != ErrorForbidden || toolError.RequestID != "request-1" {
		t.Fatalf("error=%#v", err)
	}
}
