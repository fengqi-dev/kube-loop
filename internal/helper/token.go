package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func EnsureUserToken() (string, error) {
	path, err := TokenPath()
	if err != nil {
		return "", err
	}
	if raw, readErr := os.ReadFile(path); readErr == nil {
		token := strings.TrimSpace(string(raw))
		if token != "" {
			return token, nil
		}
	}
	token, err := utils.RandomHexToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	//nolint:gosec // Token directories need owner execute permission for traversal.
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil && runtime.GOOS != goosWindows {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func ReadUserToken() (string, error) {
	path, err := TokenPath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("empty helper token")
	}
	return token, nil
}

func WriteSystemAuth(auth AuthFile) error {
	//nolint:gosec // The system state directory is traversable; token and auth files remain mode 0600.
	if err := os.MkdirAll(SystemStateDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(SystemAuthPath(), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.WriteFile(SystemTokenPath(), []byte(auth.Token+"\n"), 0o600)
}

func ReadSystemAuth() (AuthFile, error) {
	raw, err := os.ReadFile(SystemAuthPath())
	if err != nil {
		return AuthFile{}, err
	}
	var auth AuthFile
	if err := json.Unmarshal(raw, &auth); err != nil {
		return AuthFile{}, err
	}
	if auth.Token == "" {
		return AuthFile{}, fmt.Errorf("empty system helper auth")
	}
	return auth, nil
}
