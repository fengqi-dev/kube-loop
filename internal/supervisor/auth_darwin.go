//go:build darwin

package supervisor

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Auth struct {
	TokenSHA256 string `json:"tokenSha256"`
	UID         int    `json:"uid"`
}

func NewAuth(token string, uid int) (Auth, error) {
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) < 32 {
		return Auth{}, fmt.Errorf("supervisor token must be hexadecimal and contain at least 256 bits")
	}
	if uid < 0 {
		return Auth{}, fmt.Errorf("supervisor UID must not be negative")
	}
	sum := sha256.Sum256([]byte(token))
	return Auth{TokenSHA256: hex.EncodeToString(sum[:]), UID: uid}, nil
}

func (a Auth) Authorize(token string, peerUID int) bool {
	if token == "" || peerUID != a.UID {
		return false
	}
	want, err := hex.DecodeString(a.TokenSHA256)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], want) == 1
}

func WriteAuth(config Config, auth Auth) error {
	if err := os.MkdirAll(config.StateDir, 0o700); err != nil {
		return fmt.Errorf("create supervisor state: %w", err)
	}
	raw, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal supervisor auth: %w", err)
	}
	tmp := config.AuthPath() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("stage supervisor auth: %w", err)
	}
	if err := os.Rename(tmp, config.AuthPath()); err != nil {
		return fmt.Errorf("replace supervisor auth: %w", err)
	}
	return syncDir(filepath.Dir(config.AuthPath()))
}

func ReadAuth(config Config) (Auth, error) {
	raw, err := os.ReadFile(config.AuthPath())
	if err != nil {
		return Auth{}, fmt.Errorf("read supervisor auth: %w", err)
	}
	var auth Auth
	decoder := json.NewDecoder(newBytesReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&auth); err != nil {
		return Auth{}, fmt.Errorf("decode supervisor auth: %w", err)
	}
	if _, err := hex.DecodeString(auth.TokenSHA256); err != nil || len(auth.TokenSHA256) != sha256.Size*2 {
		return Auth{}, fmt.Errorf("invalid supervisor token hash")
	}
	return auth, nil
}
