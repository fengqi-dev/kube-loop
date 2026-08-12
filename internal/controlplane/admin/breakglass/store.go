// Package breakglass verifies the deployment-mounted emergency credential and
// derives a non-secret generation used to invalidate Management Sessions after
// Secret rotation. It does not issue sessions or grant ordinary Gateway access.
package breakglass

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net/netip"
	"os"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/config"
)

const maximumCredentialFileBytes = 512

var (
	ErrInvalidCredential = errors.New("management emergency authentication failed")
	ErrUnavailable       = errors.New("management emergency authentication is unavailable")
)

type Store struct {
	enabled     bool
	secretFile  string
	sessionTTL  time.Duration
	sourceCIDRs []netip.Prefix
	readFile    func(string) ([]byte, error)
}

var _ adminauthorization.BreakGlassStateReader = (*Store)(nil)

func New(config adminconfig.BreakGlassConfig) (*Store, error) {
	store := &Store{
		enabled: config.Enabled, secretFile: config.SecretFile,
		sessionTTL: config.ParsedSessionTTL(), sourceCIDRs: config.ParsedSourceCIDRs(), readFile: readBoundedCredentialFile,
	}
	if !store.enabled {
		return store, nil
	}
	if store.secretFile == "" || store.sessionTTL < adminconfig.MinimumBreakGlassSessionTTL ||
		store.sessionTTL > adminconfig.DefaultBreakGlassSessionTTL {
		return nil, ErrUnavailable
	}
	return store, nil
}

func (store *Store) SessionTTL() time.Duration {
	if store == nil {
		return 0
	}
	return store.sessionTTL
}

// Verify consumes and clears the supplied base64url credential. Successful
// verification returns only the SHA-256 generation, never the credential.
func (store *Store) Verify(ctx context.Context, source netip.Addr, supplied []byte) (string, error) {
	defer clear(supplied)
	if store == nil || !store.enabled || !store.sourceAllowed(source) {
		return "", ErrInvalidCredential
	}
	if err := ctx.Err(); err != nil {
		return "", ErrUnavailable
	}
	actual, err := decodeCredential(supplied)
	if err != nil {
		return "", ErrInvalidCredential
	}
	defer clear(actual)
	expected, generation, err := store.currentCredential()
	if err != nil {
		return "", ErrUnavailable
	}
	defer clear(expected)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return "", ErrInvalidCredential
	}
	return generation, nil
}

func (store *Store) CurrentBreakGlassState(ctx context.Context) (adminauthorization.BreakGlassState, error) {
	if store == nil || !store.enabled {
		return adminauthorization.BreakGlassState{}, nil
	}
	if err := ctx.Err(); err != nil {
		return adminauthorization.BreakGlassState{}, ErrUnavailable
	}
	credential, generation, err := store.currentCredential()
	clear(credential)
	if err != nil {
		return adminauthorization.BreakGlassState{}, ErrUnavailable
	}
	return adminauthorization.BreakGlassState{Enabled: true, Generation: generation}, nil
}

func (store *Store) currentCredential() ([]byte, string, error) {
	if store.readFile == nil {
		return nil, "", ErrUnavailable
	}
	raw, err := store.readFile(store.secretFile)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	defer clear(raw)
	if len(raw) == 0 || len(raw) > maximumCredentialFileBytes {
		return nil, "", ErrUnavailable
	}
	credential, err := decodeCredential(bytes.TrimSpace(raw))
	if err != nil {
		return nil, "", ErrUnavailable
	}
	digest := sha256.Sum256(credential)
	generation := base64.RawURLEncoding.EncodeToString(digest[:])
	return credential, generation, nil
}

func (store *Store) sourceAllowed(source netip.Addr) bool {
	if len(store.sourceCIDRs) == 0 {
		return true
	}
	if !source.IsValid() {
		return false
	}
	source = source.Unmap()
	for _, prefix := range store.sourceCIDRs {
		if prefix.Contains(source) {
			return true
		}
	}
	return false
}

func decodeCredential(raw []byte) ([]byte, error) {
	if len(raw) < 43 || len(raw) > 86 || containsWhitespace(raw) {
		return nil, ErrInvalidCredential
	}
	decoded := make([]byte, base64.RawURLEncoding.DecodedLen(len(raw)))
	count, err := base64.RawURLEncoding.Decode(decoded, raw)
	if err != nil {
		clear(decoded)
		return nil, ErrInvalidCredential
	}
	decoded = decoded[:count]
	if len(decoded) < 32 || len(decoded) > 64 {
		clear(decoded)
		return nil, ErrInvalidCredential
	}
	return decoded, nil
}

func containsWhitespace(value []byte) bool {
	for _, character := range value {
		switch character {
		case ' ', '\t', '\r', '\n':
			return true
		}
	}
	return false
}

func readBoundedCredentialFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maximumCredentialFileBytes+1))
}
