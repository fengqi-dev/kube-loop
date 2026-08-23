package credentials

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	serviceName                     = "KubeLoop"
	developmentServiceName          = "KubeLoop Dev"
	credentialMetadataSchemaVersion = 1
)

var ErrNotFound = errors.New("credentials not found")

type Credential struct {
	TokenType        string
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	DeviceID         string
	IdentityID       string
	UserName         string
}

type Store interface {
	Set(profileID string, credential Credential) error
	Get(profileID string) (Credential, error)
	Delete(profileID string) error
}

type Backend interface {
	Set(service, account, secret string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

type SystemStore struct {
	backend Backend
	random  func([]byte) error
	service string
}

type systemBackend struct{}

func (systemBackend) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (systemBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

func NewSystemStore() *SystemStore {
	return NewStore(systemBackend{})
}

func NewSystemStoreForVersion(version string) *SystemStore {
	return newStore(systemBackend{}, keyringServiceForVersion(version))
}

// NewSystemStoreForClient isolates OAuth credentials by client ID while still
// allowing Desktop and TUI to share Server profiles. Refresh tokens are bound
// to the OAuth client that obtained them and must never overwrite each other.
func NewSystemStoreForClient(version, clientID string) *SystemStore {
	return newStoreForClient(systemBackend{}, version, clientID)
}

func NewStore(backend Backend) *SystemStore {
	return newStore(backend, serviceName)
}

func newStore(backend Backend, service string) *SystemStore {
	return &SystemStore{backend: backend, service: service, random: func(value []byte) error {
		_, err := rand.Read(value)
		return err
	}}
}

func newStoreForClient(backend Backend, version, clientID string) *SystemStore {
	return newStore(backend, keyringServiceForClient(version, clientID))
}

func keyringServiceForVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return developmentServiceName
	}
	return serviceName
}

func keyringServiceForClient(version, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return keyringServiceForVersion(version)
	}
	return keyringServiceForVersion(version) + " OAuth " + clientID
}

func accountPrefix(profileID string) (string, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "", errors.New("server Profile ID is required")
	}
	hash := sha256.Sum256([]byte(profileID))
	return hex.EncodeToString(hash[:]), nil
}

func credentialAccounts(prefix, generation string) []string {
	return []string{
		prefix + ":" + generation + ":access",
		prefix + ":" + generation + ":refresh",
		prefix + ":" + generation + ":metadata",
	}
}
